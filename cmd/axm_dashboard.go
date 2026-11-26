package main

import (
	"github.com/knadh/listmonk/internal/auth"
	"github.com/labstack/echo/v4"
)

// dashboardPerms are the permissions that let a user see the company-wide dashboard counts
// (LST-73): the counts of lists, subscribers, campaigns and messages. A role that reads or
// manages everything in one of those areas, or the analytics, may see the sum; a role limited
// to its own lists (list:get, list:manage) may not read what the others hold. The frontend
// keeps the same list in frontend/src/views/Dashboard.vue (DASHBOARD_PERMS).
var dashboardPerms = []string{
	auth.PermListGetAll,
	auth.PermListManageAll,
	auth.PermSubscribersGetAll,
	auth.PermCampaignsGetAll,
	auth.PermCampaignsManageAll,
	auth.PermCampaignsGetAnalytics,
}

// dashboardChartPerms: the charts are the daily views and clicks of every campaign, which is
// analytics, so they ask for what the analytics routes ask for.
var dashboardChartPerms = []string{auth.PermCampaignsGetAnalytics}

// dashboardRoutes registers the dashboard routes behind the permission check pm.
func (a *App) dashboardRoutes(g *echo.Group, pm func(echo.HandlerFunc, ...string) echo.HandlerFunc) {
	g.GET("/api/dashboard/charts", pm(a.GetDashboardCharts, dashboardChartPerms...))
	g.GET("/api/dashboard/counts", pm(a.GetDashboardCounts, dashboardPerms...))
}
