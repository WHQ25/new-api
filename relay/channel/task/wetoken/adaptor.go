package wetoken

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
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// TaskAdaptor 对接 wetoken 的可灵线路。
//
// 它没有走通用的 New API 级联适配器，是因为那个适配器把统一协议的 TaskSubmitReq
// 原样转发，而 wetoken 的可灵要的是一套扁平的顶层字段。TaskSubmitReq 是十个字段的
// 白名单，audio_generation / file_infos 这些参数在序列化时会被静默丢掉——请求照样
// 成功，只是生成的视频没有声音、也没有参考素材。
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

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "get_task_request_failed", http.StatusBadRequest)
	}
	// 必须在计价之前跑，也必须在这里而不是 ResolveVideoBilling 里：它改的是下发内容，
	// 按次价等其它计费方式下同样要生效。
	normalizeBillingDimensions(req.Metadata)
	return nil
}

// ResolveVideoBilling 让计费读到上游实际会生成的时长，并把越界的请求拦在预扣费之前，
// 见 channel.VideoBillingResolver。构造请求体与计费维度走的是同一个入口。
func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (float64, *taskdto.TaskError) {
	payload, taskErr := a.payloadFromContext(c, info)
	if taskErr != nil {
		return 0, taskErr
	}
	return float64(payload.Duration), nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/v1/video/generations", a.baseURL), nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	payload, taskErr := a.payloadFromContext(c, info)
	if taskErr != nil {
		return nil, errors.New(taskErr.Message)
	}
	data, err := common.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "marshal request payload failed")
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) payloadFromContext(c *gin.Context, info *relaycommon.RelayInfo) (*requestPayload, *taskdto.TaskError) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "get_task_request_failed", http.StatusBadRequest)
	}
	return convertToRequestPayload(&req, info)
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	var upstreamResp queryResponse
	if err := common.Unmarshal(responseBody, &upstreamResp); err != nil {
		return "", nil, service.TaskErrorWrapper(err, "unmarshal_response_failed", http.StatusInternalServerError)
	}
	if upstreamResp.Error != nil {
		return "", nil, service.TaskErrorWrapperLocal(
			fmt.Errorf("%s", upstreamResp.Error.Message),
			upstreamResp.Error.Code, http.StatusBadRequest)
	}

	upstreamTaskID := upstreamResp.TaskID
	if upstreamTaskID == "" {
		upstreamTaskID = upstreamResp.ID
	}
	if upstreamTaskID == "" {
		return "", nil, service.TaskErrorWrapper(
			errors.New("upstream returned no task id"), "invalid_upstream_response", http.StatusInternalServerError)
	}

	clientResp := dto.NewOpenAIVideo()
	clientResp.ID = info.PublicTaskID
	clientResp.TaskID = info.PublicTaskID
	clientResp.CreatedAt = time.Now().Unix()
	clientResp.Model = info.OriginModelName
	c.JSON(http.StatusOK, clientResp)

	return upstreamTaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	upstreamTaskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}
	// 查询走 /v1/videos/{id} 而不是文档里的 /v1/video/generations/{id}：后者返回的是
	// 上游自己的任务记录（{"code","data":{"status":"IN_PROGRESS","data":{...腾讯云原生...}}}），
	// 前者才是 ParseTaskResult 认的 OpenAI 视频格式。用错端点会让每次轮询都解析失败。
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/videos/%s", baseUrl, upstreamTaskID), nil)
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

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var upstream queryResponse
	if err := common.Unmarshal(respBody, &upstream); err != nil {
		return nil, errors.Wrap(err, "unmarshal upstream video failed")
	}

	taskInfo := &relaycommon.TaskInfo{TaskID: upstream.TaskID}
	taskInfo.TotalTokens = relaycommon.ParseVideoTotalTokens(respBody)

	// 上游同时在用 OpenAI 的状态词与它自己文档里的那套，两套都要认：漏掉一个终态
	// 会让任务一直轮询到超时清理才退款。
	switch upstream.Status {
	case "submitted", "queued":
		taskInfo.Status = model.TaskStatusSubmitted
		taskInfo.Progress = taskcommon.ProgressSubmitted
	case "in_progress", "processing", "running":
		taskInfo.Status = model.TaskStatusInProgress
		taskInfo.Progress = taskcommon.ProgressInProgress
	case "completed", "succeeded", "success":
		taskInfo.Status = model.TaskStatusSuccess
		taskInfo.Progress = taskcommon.ProgressComplete
		taskInfo.Url = upstream.videoURL()
	case "failed", "cancelled", "expired":
		taskInfo.Status = model.TaskStatusFailure
		if upstream.Error != nil {
			taskInfo.Reason = upstream.Error.Message
		}
		if taskInfo.Reason == "" {
			taskInfo.Reason = fmt.Sprintf("task %s", upstream.Status)
		}
	default:
		return nil, fmt.Errorf("unknown upstream status: %s", upstream.Status)
	}
	return taskInfo, nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

// ConvertToOpenAIVideo 把存下来的任务数据转成客户端要的 OpenAI 视频格式。
func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var upstream queryResponse
	if err := common.Unmarshal(originTask.Data, &upstream); err != nil {
		return nil, errors.Wrap(err, "unmarshal upstream video task data failed")
	}

	clientVideo := dto.NewOpenAIVideo()
	clientVideo.ID = originTask.TaskID
	clientVideo.TaskID = originTask.TaskID
	clientVideo.Status = originTask.Status.ToVideoStatus()
	clientVideo.SetProgressStr(originTask.Progress)
	clientVideo.CreatedAt = upstream.CreatedAt
	clientVideo.CompletedAt = upstream.CompletedAt
	// 回给客户端的必须是它请求的那个模型名，上游响应里的是模型映射之后的名字。
	clientVideo.Model = originTask.Properties.OriginModelName
	if upstream.Metadata != nil {
		clientVideo.Metadata = upstream.Metadata
	}
	if url := upstream.videoURL(); url != "" {
		clientVideo.SetMetadata("url", url)
	}
	if upstream.Error != nil {
		clientVideo.Error = upstream.Error
	}
	return common.Marshal(clientVideo)
}
