package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	// Video proxy: accepts either session auth (dashboard) or token auth (API clients)
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
	}

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		videoV1Router.POST("/video/generations", controller.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
	}

	// Ark (火山引擎方舟 / 豆包 Seedance) 官方视频协议入站兼容层：
	// 客户端可以只改 base URL 就把官方 SDK 指向本网关。
	// ArkRequestConvert 必须排在 Distribute 之前——它把请求体改写成内部统一形状，
	// Distribute 才能从 body 顶层的 model 选出渠道。
	arkVideoRouter := router.Group(middleware.ArkVideoTaskPath)
	arkVideoRouter.Use(middleware.RouteTag("relay"))
	arkVideoRouter.Use(middleware.ArkRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		arkVideoRouter.POST("", controller.RelayTask)
		arkVideoRouter.GET("/:task_id", controller.RelayTaskFetch)
	}

	// 可灵 3.0 / 3.0 Omni 官方协议入站兼容层。官方把型号编在路径里，请求体没有 model
	// 字段，所以 KlingV3RequestConvert 必须排在 Distribute 之前——它按路径推导出模型名
	// 写进 body 顶层，Distribute 才能据此选出渠道。
	klingV3Router := router.Group("")
	klingV3Router.Use(middleware.RouteTag("relay"))
	klingV3Router.Use(middleware.KlingV3RequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingV3Router.POST(middleware.KlingV3TextToVideoPath, controller.RelayTask)
		klingV3Router.POST(middleware.KlingV3ImageToVideoPath, controller.RelayTask)
		klingV3Router.POST(middleware.KlingV3OmniVideoPath, controller.RelayTask)
		klingV3Router.GET(middleware.KlingV3TasksPath, controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", controller.RelayTask)
		klingV1Router.POST("/videos/image2video", controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", controller.RelayTask)
	}
}
