package common

import "strings"

// 统一协议的素材类型与角色，取自方舟 Seedance 的 content[] 契约。计费的档位判定和
// 适配器构造的上游请求体都从这里取值：两边各自解析一遍 metadata.content，就会出现
// 按「无参考视频」计费、却把参考视频下发给上游的组合。
const (
	TaskMediaTypeImage = "image_url"
	TaskMediaTypeVideo = "video_url"
	TaskMediaTypeAudio = "audio_url"
)

const (
	TaskMediaRoleFirstFrame     = "first_frame"
	TaskMediaRoleLastFrame      = "last_frame"
	TaskMediaRoleReferenceImage = "reference_image"
	TaskMediaRoleReferenceVideo = "reference_video"
	TaskMediaRoleReferenceAudio = "reference_audio"
)

// TaskMediaItem 是 metadata.content 里的一个素材项。Role 可能为空——首帧模式下方舟
// 允许省略 role，此时按素材类型的默认角色处理。
type TaskMediaItem struct {
	Type string
	Role string
	URL  string
}

// MediaItems 解析统一协议的 metadata.content，只返回素材项，跳过 text 项。
//
// 键名一律按小写匹配：storeTaskRequest 会先跑 CanonicalizeMetadataKeys 把每一层的键
// 都小写化，所以 {"TYPE":"video_url"} 到这里已经是 type。
func (t *TaskSubmitReq) MediaItems() []TaskMediaItem {
	contentRaw, ok := MetadataValue(t.Metadata, "content")
	if !ok {
		return nil
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return nil
	}
	items := make([]TaskMediaItem, 0, len(contentSlice))
	for _, raw := range contentSlice {
		if item, ok := taskMediaItemFrom(raw); ok {
			items = append(items, item)
		}
	}
	return items
}

// HasMediaType 报告 metadata.content 里有没有某一类素材。
func (t *TaskSubmitReq) HasMediaType(mediaType string) bool {
	for _, item := range t.MediaItems() {
		if item.Type == mediaType {
			return true
		}
	}
	return false
}

// taskMediaItemFrom 读出一个素材项。URL 既接受方舟官方的嵌套写法
// {"type":"video_url","video_url":{"url":"..."}}，也接受把地址直接写在同名键上的
// 扁平写法——统一协议入站的调用方两种都在用，只认一种会把另一种整项丢掉。
func taskMediaItemFrom(raw interface{}) (TaskMediaItem, bool) {
	itemMap, ok := raw.(map[string]interface{})
	if !ok {
		return TaskMediaItem{}, false
	}
	itemType, _ := itemMap["type"].(string)
	itemType = strings.ToLower(strings.TrimSpace(itemType))
	role, _ := itemMap["role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))

	for _, mediaType := range []string{TaskMediaTypeImage, TaskMediaTypeVideo, TaskMediaTypeAudio} {
		value, has := itemMap[mediaType]
		if !has {
			continue
		}
		// 同名键在场就说明这是该类素材，哪怕 type 缺失或与之不符。
		return TaskMediaItem{Type: mediaType, Role: role, URL: taskMediaURL(value)}, true
	}

	switch itemType {
	case TaskMediaTypeImage, TaskMediaTypeVideo, TaskMediaTypeAudio:
		// 只声明了 type、地址却没跟上来的项仍然算数：计费据此判档，漏掉它会让
		// 一个上游会当成参考视频处理的请求按纯文生视频计费。
		return TaskMediaItem{Type: itemType, Role: role, URL: taskMediaURL(itemMap["url"])}, true
	}
	return TaskMediaItem{}, false
}

func taskMediaURL(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]interface{}:
		url, _ := typed["url"].(string)
		return strings.TrimSpace(url)
	}
	return ""
}
