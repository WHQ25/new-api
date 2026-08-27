package doubao

import "strings"

var ModelList = []string{
	"doubao-seedance-1-0-pro-250528",
	"doubao-seedance-1-0-lite-t2v",
	"doubao-seedance-1-0-lite-i2v",
	"doubao-seedance-1-5-pro-251215",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
}

var ChannelName = "doubao-video"

// videoPriceKey 价格表的键：输出分辨率档（is1080p/is4k 均为 false 即 480p/720p 基准档）、输入是否含视频。
type videoPriceKey struct {
	is1080p  bool
	is4k     bool
	hasVideo bool
}

// videoPriceTable 各模型在不同 (输出分辨率档, 是否含视频输入) 下的单价（元/百万 token）。
// 其中零值键 {480p/720p, 不含视频} 为基准价，等于管理员应配置的 ModelRatio；
// 计费时取 实际单价/基准价 作为 OtherRatio。
var videoPriceTable = map[string]map[videoPriceKey]float64{
	"doubao-seedance-2-0-260128": {
		{hasVideo: false}:                46.0,
		{hasVideo: true}:                 28.0,
		{is1080p: true, hasVideo: false}: 51.0,
		{is1080p: true, hasVideo: true}:  31.0,
		{is4k: true, hasVideo: false}:    26.0,
		{is4k: true, hasVideo: true}:     16.0,
	},
	"doubao-seedance-2-0-fast-260128": {
		{hasVideo: false}: 37.0,
		{hasVideo: true}:  22.0,
	},
}

// GetVideoInputRatio 返回指定模型在给定输出分辨率/是否含视频输入下，相对基准价的计费倍率。
// 第二个返回值表示该模型是否配置了价格表；倍率为 1.0 时调用方可忽略该 OtherRatio。
func GetVideoInputRatio(modelName, resolution string, hasVideo bool) (float64, bool) {
	prices, ok := videoPriceTable[modelName]
	base := prices[videoPriceKey{}] // 零值键 = {480p/720p, 不含视频} 基准价
	if !ok || base <= 0 {
		return 0, false
	}
	res := strings.ToLower(strings.TrimSpace(resolution))
	price, ok := prices[videoPriceKey{is1080p: res == "1080p", is4k: res == "4k", hasVideo: hasVideo}]
	if !ok {
		// 未配置的组合（如 fast 无 1080p/4k，上游会自行报错）按基准价计费即可。
		return 1.0, true
	}
	return price / base, true
}

// videoDurationLimit 描述某一档 Seedance 的时长与音频契约。
// 来源：https://docs.volcengine.com/docs/82379/1520757
type videoDurationLimit struct {
	// minSeconds / maxSeconds 是 duration 取值区间。区间外的显式时长要在这里 400：
	// 通用校验只拦 3600 秒，Seedance 2.0 请求 duration=3600 会先按 3600 秒预扣，
	// 再等上游拒绝退款。duration=-1（模型自选时长）按 maxSeconds 预扣，
	// 再靠上游回报的 usage 做差额结算退回。
	minSeconds int
	maxSeconds int
	// supportsSelfSelect 表示该档接受 duration = -1。不能用 maxSeconds > 0 代替：
	// 每一档都有时长上界，但只有 1.5 及以后的代次允许模型自选。
	supportsSelfSelect bool
	// selfSelectByDefault 表示不传 duration 时也由模型自选时长（即官方默认值为 -1）。
	selfSelectByDefault bool
	// supportsFrames 表示该档支持用 frames 指定帧数，且优先级高于 duration。
	supportsFrames bool
	// generatesAudioByDefault 表示 generate_audio 的官方默认值为 true，
	// 即客户端什么都不传，上游也会生成有声视频。
	generatesAudioByDefault bool
}

// videoDurationLimits 按从新到旧、从具体到笼统列出各档 Seedance，逐条子串匹配。
// 用切片而不是 map，是因为匹配靠子串包含，map 的迭代顺序随机会让「同时匹配两条」的
// 模型名每次落到不同的档；顺序也因此有意义——seedance10pro 必须排在 seedance10 前面，
// 否则 1.0 lite 会跟着 1.0 pro 一起被放行 frames。
var videoDurationLimits = []struct {
	generation string
	limit      videoDurationLimit
}{
	{"seedance25", videoDurationLimit{minSeconds: 4, maxSeconds: 30, supportsSelfSelect: true, selfSelectByDefault: true, generatesAudioByDefault: true}},
	{"seedance20", videoDurationLimit{minSeconds: 4, maxSeconds: 15, supportsSelfSelect: true, generatesAudioByDefault: true}},
	{"seedance15", videoDurationLimit{minSeconds: 4, maxSeconds: 12, supportsSelfSelect: true, generatesAudioByDefault: true}},
	// frames 只有 Seedance 1.0 pro / 1.0 pro fast 支持，1.0 lite 不支持。
	{"seedance10pro", videoDurationLimit{minSeconds: 2, maxSeconds: 12, supportsFrames: true}},
	{"seedance10", videoDurationLimit{minSeconds: 2, maxSeconds: 12}},
}

// videoDurationLimitFor 取模型所属代次的时长契约，第二个返回值表示是否认出了代次。
func videoDurationLimitFor(modelName string) (videoDurationLimit, bool) {
	compact := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToLower(modelName))
	for _, entry := range videoDurationLimits {
		if strings.Contains(compact, entry.generation) {
			return entry.limit, true
		}
	}
	return videoDurationLimit{}, false
}

// 官方 frames 契约：取值区间 [29, 289]，且必须满足 frames = 25 + 4n，
// 换算出的时长为 frames/24 秒（1.2083 ~ 12.0417 秒）。
// 来源：https://docs.volcengine.com/docs/82379/1520757
// arkEndpointIDPrefix 是方舟推理接入点 ID 的前缀。它可以直接当模型名用，
// 但串里不含型号信息，认不出该套哪一档契约。
const arkEndpointIDPrefix = "ep-"

const (
	// videoFramesPerSecond 是方舟视频的固定帧率，frames 与时长按它换算。
	videoFramesPerSecond = 24
	videoFramesMin       = 29
	videoFramesMax       = 289
	videoFramesBase      = 25
	videoFramesStep      = 4
)
