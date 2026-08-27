package common

import "github.com/QuantumNous/new-api/constant"

// EndpointInfo 描述单个端点的默认请求信息
// path: 上游路径
// method: HTTP 请求方式，例如 POST/GET
// 目前均为 POST，后续可扩展
//
// json 标签用于直接序列化到 API 输出
// 例如：{"path":"/v1/chat/completions","method":"POST"}

type EndpointInfo struct {
	Path   string `json:"path"`
	Method string `json:"method"`
}

// defaultEndpointInfoMap 保存内置端点的默认 Path 与 Method
var defaultEndpointInfoMap = map[constant.EndpointType]EndpointInfo{
	constant.EndpointTypeOpenAI:                {Path: "/v1/chat/completions", Method: "POST"},
	constant.EndpointTypeOpenAIResponse:        {Path: "/v1/responses", Method: "POST"},
	constant.EndpointTypeOpenAIResponseCompact: {Path: "/v1/responses/compact", Method: "POST"},
	constant.EndpointTypeOpenAIAlphaSearch:     {Path: "/v1/alpha/search", Method: "POST"},
	constant.EndpointTypeAnthropic:             {Path: "/v1/messages", Method: "POST"},
	constant.EndpointTypeGemini:                {Path: "/v1beta/models/{model}:generateContent", Method: "POST"},
	constant.EndpointTypeJinaRerank:            {Path: "/v1/rerank", Method: "POST"},
	constant.EndpointTypeImageGeneration:       {Path: "/v1/images/generations", Method: "POST"},
	constant.EndpointTypeEmbeddings:            {Path: "/v1/embeddings", Method: "POST"},
	// 没有这一条，模型广场就拿不到视频端点的路径：详情页的调用示例按
	// `Boolean(e.path)` 过滤，视频模型会一个示例都不显示。
	// 用 /v1/video/generations 而不是 OpenAI 形状的 /v1/videos：所有视频任务渠道都
	// 支持它，而 /v1/videos 的响应转换是逐适配器实现的，未实现的会返回 not_implemented。
	constant.EndpointTypeOpenAIVideo: {Path: "/v1/video/generations", Method: "POST"},
	// 方舟官方视频协议的入站兼容层，路由注册在 middleware.ArkVideoTaskPath。common
	// 不能反向依赖 middleware，所以路径在这里重复了一次；两者一致由
	// router.TestArkVideoDefaultEndpointMatchesRegisteredRoute 守住。
	constant.EndpointTypeArkVideo: {Path: "/api/v3/contents/generations/tasks", Method: "POST"},
	// 可灵 3.0 官方视频协议的入站兼容层，路由注册在 middleware.KlingV3TextToVideoPath。
	// 同样不能反向依赖 middleware，一致性由 router 的路由注册测试守住。
	constant.EndpointTypeKlingVideo: {Path: "/text-to-video/kling-3.0", Method: "POST"},
}

// GetDefaultEndpointInfo 返回指定端点类型的默认信息以及是否存在
func GetDefaultEndpointInfo(et constant.EndpointType) (EndpointInfo, bool) {
	info, ok := defaultEndpointInfoMap[et]
	return info, ok
}
