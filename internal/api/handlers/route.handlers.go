package routes

import (
	"log"

	"github.com/gin-gonic/gin"
)

// SetupRouteHandlers registers the route management endpoints
func SetupRouteHandlers(router *gin.RouterGroup) {
	routeGroup := router.Group("/route")

	routeGroup.GET("/start", StartRoute)
	routeGroup.GET("/stop", StopRoute)
	routeGroup.GET("/pause", PauseRoute)
}

// StartRoute handles the start route endpoint
func StartRoute(c *gin.Context) {
	log.Println("Route start endpoint called")
	c.JSON(200, gin.H{
		"status":  "success",
		"message": "Route started",
	})
}

// StopRoute handles the stop route endpoint
func StopRoute(c *gin.Context) {
	log.Println("Route stop endpoint called")
	c.JSON(200, gin.H{
		"status":  "success",
		"message": "Route stopped",
	})
}

// PauseRoute handles the pause route endpoint
func PauseRoute(c *gin.Context) {
	log.Println("Route pause endpoint called")
	c.JSON(200, gin.H{
		"status":  "success",
		"message": "Route paused",
	})
}
