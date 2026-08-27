package wetoken

import "github.com/QuantumNous/new-api/setting/billing_setting"

const ChannelName = "wetoken"

// 上游契约见 https://wetoken.top/model-docs?model=kling-v3 与 ?model=kling-v3-omni。
// 两个模型共用 POST /v1/video/generations，请求体是扁平的顶层字段，与可灵开放平台
// 的原生协议不同——wetoken 自己做了一层翻译，我们对接的是翻译之后的这一套。
const (
	defaultDurationSeconds = 5
	minDurationSeconds     = 3
	maxDurationSeconds     = 15
)

// file_infos 的取值域。
const (
	fileInfoTypeURL        = "Url"
	fileInfoCategoryImage  = "Image"
	fileInfoCategoryVideo  = "Video"
	fileInfoUsageFirstFame = "FirstFrame"
	fileInfoUsageReference = "Reference"
)

// modelContract 是同一个端点上各模型的能力差异。越界的组合必须在计价之前拒掉：
// 预扣之后再由上游 400，差额要走退款，而按最贵的档位预扣本身就已经算错了。
type modelContract struct {
	// supportsVideoInput 表示能不能收参考视频（file_infos[].Category = Video）。
	supportsVideoInput bool
	// supportsReferenceImage 表示能不能收 Usage = Reference 的参考图。
	// kling-v3 只有首帧模式，参考图是 omni 才有的能力。
	supportsReferenceImage bool
}

var modelContracts = map[string]modelContract{
	"kling-v3":      {},
	"kling-v3-omni": {supportsVideoInput: true, supportsReferenceImage: true},
}

// ModelList 只列出已经核对过契约的模型。认不出的模型会在校验阶段被拒，而不是
// 按 kling-v3 的契约猜一个请求体发出去。
var ModelList = []string{"kling-v3", "kling-v3-omni"}

// supportedResolutions 把计费的档位字面量映射成上游要的写法。可灵没有 480p 档，
// 而通用的 ParseVideoTokenTier 认它：不在这里拦下，价目表配了 sec:480p 的部署会
// 先成交再被上游拒绝。
var supportedResolutions = map[string]string{
	billing_setting.VideoTokenTier720p:  "720P",
	billing_setting.VideoTokenTier1080p: "1080P",
	billing_setting.VideoTokenTier4K:    "4K",
}
