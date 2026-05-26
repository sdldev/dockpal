package server

import "github.com/gin-gonic/gin"

type roleRouterWrapper struct {
	viewerGroup   *gin.RouterGroup
	operatorGroup *gin.RouterGroup
	adminGroup    *gin.RouterGroup
}

func (r *roleRouterWrapper) GET(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.viewerGroup.GET(path, handlers...)
}

func (r *roleRouterWrapper) POST(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	if path == "/system/update" {
		return r.adminGroup.POST(path, handlers...)
	}
	return r.operatorGroup.POST(path, handlers...)
}

func (r *roleRouterWrapper) PUT(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.operatorGroup.PUT(path, handlers...)
}

func (r *roleRouterWrapper) PATCH(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.operatorGroup.PATCH(path, handlers...)
}

func (r *roleRouterWrapper) DELETE(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.operatorGroup.DELETE(path, handlers...)
}