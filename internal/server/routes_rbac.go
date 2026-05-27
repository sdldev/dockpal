package server

import "github.com/gin-gonic/gin"

type roleRouterWrapper struct {
	viewerGroup        *gin.RouterGroup
	operatorGroup      *gin.RouterGroup
	adminGroup         *gin.RouterGroup
	mutationMiddleware gin.HandlerFunc
}

func (r *roleRouterWrapper) GET(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.viewerGroup.GET(path, handlers...)
}

func (r *roleRouterWrapper) POST(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	handlers = r.withMutationLimit(handlers)
	if path == "/system/update" {
		return r.adminGroup.POST(path, handlers...)
	}
	return r.operatorGroup.POST(path, handlers...)
}

func (r *roleRouterWrapper) PUT(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.operatorGroup.PUT(path, r.withMutationLimit(handlers)...)
}

func (r *roleRouterWrapper) PATCH(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.operatorGroup.PATCH(path, r.withMutationLimit(handlers)...)
}

func (r *roleRouterWrapper) DELETE(path string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.operatorGroup.DELETE(path, r.withMutationLimit(handlers)...)
}

func (r *roleRouterWrapper) withMutationLimit(handlers []gin.HandlerFunc) []gin.HandlerFunc {
	if r.mutationMiddleware == nil {
		return handlers
	}
	limited := make([]gin.HandlerFunc, 0, len(handlers)+1)
	limited = append(limited, r.mutationMiddleware)
	limited = append(limited, handlers...)
	return limited
}
