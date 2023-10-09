package restapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/iotaledger/inx-app/pkg/httpserver"
)

const (
	nodeAPIHealthRoute = "/health"

	nodeAPIRoutesRoute = "/api/routes"
)

type RoutesResponse struct {
	Routes []string `json:"routes"`
}

func setupRoutes() {

	deps.Echo.GET(nodeAPIHealthRoute, func(c echo.Context) error {
		if deps.Protocol.MainEngine.Get().SyncManager.IsNodeSynced() {
			return c.NoContent(http.StatusOK)
		}

		return c.NoContent(http.StatusServiceUnavailable)
	})

	deps.Echo.GET(nodeAPIRoutesRoute, func(c echo.Context) error {
		resp := &RoutesResponse{
			Routes: deps.RestRouteManager.Routes(),
		}

		return httpserver.JSONResponse(c, http.StatusOK, resp)
	})
}
