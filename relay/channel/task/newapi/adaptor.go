package newapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// TaskAdaptor implements channel.TaskAdaptor for cascading task requests to upstream new-api instances.
type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction validates the task submission request.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

// BuildRequestURL constructs the upstream task submission endpoint.
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/v1/video/generations", a.baseURL), nil
}

// BuildRequestHeader sets Authorization header for upstream new-api.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// BuildRequestBody marshals the TaskSubmitReq as-is to upstream.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	// Map model if needed
	if info.IsModelMapped {
		req.Model = info.UpstreamModelName
	} else if req.Model != "" {
		info.UpstreamModelName = req.Model
	}

	data, err := common.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, "marshal task request failed")
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common task API request helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse parses the upstream task submission response.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}

	// Upstream new-api returns OpenAIVideo format on submission
	var upstreamResp dto.OpenAIVideo
	err = common.Unmarshal(responseBody, &upstreamResp)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "unmarshal_response_failed", http.StatusInternalServerError)
		return
	}

	if upstreamResp.Error != nil {
		taskErr = service.TaskErrorWrapperLocal(
			fmt.Errorf("%s", upstreamResp.Error.Message),
			upstreamResp.Error.Code,
			http.StatusBadRequest,
		)
		return
	}

	// Return our own public task ID to client
	clientResp := dto.NewOpenAIVideo()
	clientResp.ID = info.PublicTaskID
	clientResp.TaskID = info.PublicTaskID
	clientResp.CreatedAt = time.Now().Unix()
	clientResp.Model = info.OriginModelName

	c.JSON(http.StatusOK, clientResp)

	// Store upstream task_id for polling
	return upstreamResp.TaskID, responseBody, nil
}

// FetchTask queries upstream new-api for task status.
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	upstreamTaskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	url := fmt.Sprintf("%s/v1/videos/%s", baseUrl, upstreamTaskID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

// ParseTaskResult maps upstream OpenAIVideo status to internal TaskInfo.
func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var upstreamVideo dto.OpenAIVideo
	err := common.Unmarshal(respBody, &upstreamVideo)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal upstream video failed")
	}

	taskInfo := &relaycommon.TaskInfo{
		TaskID: upstreamVideo.TaskID,
	}

	taskInfo.TotalTokens = relaycommon.ParseVideoTotalTokens(respBody)

	// Map upstream status to internal status
	switch upstreamVideo.Status {
	case "submitted", "queued":
		taskInfo.Status = model.TaskStatusSubmitted
		taskInfo.Progress = taskcommon.ProgressSubmitted
	case "in_progress", "processing":
		taskInfo.Status = model.TaskStatusInProgress
		taskInfo.Progress = taskcommon.ProgressInProgress
	case "completed", "succeeded":
		taskInfo.Status = model.TaskStatusSuccess
		taskInfo.Progress = taskcommon.ProgressComplete
		// Extract video URL from metadata
		if meta := upstreamVideo.Metadata; meta != nil {
			if urlVal, ok := meta["url"]; ok {
				if urlStr, ok := urlVal.(string); ok {
					taskInfo.Url = urlStr
				}
			}
		}
	case "failed", "cancelled", "expired":
		// cancelled/expired 同样是终态：漏判会让任务一直轮询到超时清理才退款。
		taskInfo.Status = model.TaskStatusFailure
		if upstreamVideo.Error != nil {
			taskInfo.Reason = upstreamVideo.Error.Message
		}
		if taskInfo.Reason == "" {
			taskInfo.Reason = fmt.Sprintf("task %s", upstreamVideo.Status)
		}
	default:
		return nil, fmt.Errorf("unknown upstream status: %s", upstreamVideo.Status)
	}

	return taskInfo, nil
}

func (a *TaskAdaptor) GetModelList() []string {
	// Model list is dynamically fetched from upstream /v1/models
	return []string{}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "newapi"
}

// ConvertToOpenAIVideo converts stored task data to client-facing OpenAIVideo format.
func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var upstreamVideo dto.OpenAIVideo
	if err := common.Unmarshal(originTask.Data, &upstreamVideo); err != nil {
		return nil, errors.Wrap(err, "unmarshal upstream video task data failed")
	}

	// Map to client format
	clientVideo := dto.NewOpenAIVideo()
	clientVideo.ID = originTask.TaskID
	clientVideo.Status = originTask.Status.ToVideoStatus()
	clientVideo.SetProgressStr(originTask.Progress)
	clientVideo.CreatedAt = upstreamVideo.CreatedAt
	clientVideo.CompletedAt = upstreamVideo.CompletedAt

	// Copy metadata
	if upstreamVideo.Metadata != nil {
		clientVideo.Metadata = upstreamVideo.Metadata
	}

	if upstreamVideo.Error != nil {
		clientVideo.Error = upstreamVideo.Error
	}

	return common.Marshal(clientVideo)
}
