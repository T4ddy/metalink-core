package routes

import (
	"github.com/gin-gonic/gin"
)

// SetupMainHandlers registers the main application endpoints
func SetupMainHandlers(router *gin.RouterGroup, config map[string]string) {
	router.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"port":     config["port"],
			"dbUrl":    config["dbUrl"],
			"redisUrl": config["redisUrl"],
		})
	})

	router.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"test": "test",
		})
	})
}
