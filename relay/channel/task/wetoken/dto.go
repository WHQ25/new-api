package wetoken

import "github.com/QuantumNous/new-api/relaykit/dto"

// requestPayload 是 wetoken 的 POST /v1/video/generations 请求体。
//
// 计费维度（duration / size / audio_generation / file_infos）由适配器算出后覆盖，
// 其余字段可以由客户端通过 metadata 原样透传：这些参数不参与计价，上游认得，
// 挡在这一层只会让功能凭空少一块。
type requestPayload struct {
	Model    string `json:"model"`
	Prompt   string `json:"prompt,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Size     string `json:"size,omitempty"`

	// AudioGeneration 必须是指针：上游默认 false，非指针的 bool 配 omitempty 会
	// 让显式的 audio_generation:false 在 marshal 时消失，与「没传」无法区分。
	AudioGeneration *bool `json:"audio_generation,omitempty"`

	FileInfos    []fileInfo `json:"file_infos,omitempty"`
	LastFrameURL string     `json:"last_frame_url,omitempty"`
	AspectRatio  string     `json:"aspect_ratio,omitempty"`

	NegativePrompt        string        `json:"negative_prompt,omitempty"`
	EnhancePrompt         string        `json:"enhance_prompt,omitempty"`
	InputRegion           string        `json:"input_region,omitempty"`
	ExtInfo               string        `json:"ext_info,omitempty"`
	SubjectInfos          []subjectInfo `json:"subject_infos,omitempty"`
	InputComplianceCheck  string        `json:"input_compliance_check,omitempty"`
	OutputComplianceCheck string        `json:"output_compliance_check,omitempty"`
	LogoAdd               string        `json:"logo_add,omitempty"`
	SessionContext        string        `json:"session_context,omitempty"`
}

// fileInfo 的字段名在上游是首字母大写的。Go 的反序列化对大小写不敏感，所以
// metadata 透传进来的小写键（storeTaskRequest 会把 metadata 的键全部小写化）
// 仍然能落到这里，marshal 时再按上游要的写法发出去。
type fileInfo struct {
	Type     string `json:"Type"`
	Category string `json:"Category,omitempty"`
	Url      string `json:"Url"`
	Usage    string `json:"Usage,omitempty"`
}

type subjectInfo struct {
	Id string `json:"Id"`
}

// queryResponse 是任务查询的响应。上游在 OpenAI 视频格式之外还可能把产物地址放在
// 顶层 url 上，两处都要读：只认其中一处会让任务卡在轮询直到超时清理。
type queryResponse struct {
	dto.OpenAIVideo
	URL string `json:"url,omitempty"`
}

// videoURL 返回产物地址，顶层优先。
func (r *queryResponse) videoURL() string {
	if r.URL != "" {
		return r.URL
	}
	if r.Metadata == nil {
		return ""
	}
	url, _ := r.Metadata["url"].(string)
	return url
}
