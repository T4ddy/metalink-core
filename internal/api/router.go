package api

import (
	routes "metalink/internal/api/handlers"

	"github.com/gin-gonic/gin"
)

// SetupRouter initializes all application routes
func SetupRouter(r *gin.Engine, config map[string]string) {
	// API group
	api := r.Group("/api")

	// Setup main handlers
	routes.SetupMainHandlers(r.Group(""), config)

	// Setup route handlers
	routes.SetupRouteHandlers(api)
}
