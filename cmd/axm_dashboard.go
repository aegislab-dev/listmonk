package main

import "github.com/knadh/listmonk/internal/auth"

// dashboardPerms are the permissions that let a user see the company-wide dashboard numbers
// (LST-73): the counts of lists, subscribers, campaigns and messages, and the charts. A role
// that reads or manages everything in one of those areas, or the analytics, may see the sum; a
// role limited to its own lists (list:get, list:manage) may not read what the others hold. The
// frontend keeps the same list in frontend/src/views/Dashboard.vue (DASHBOARD_PERMS).
var dashboardPerms = []string{
	auth.PermListGetAll,
	auth.PermListManageAll,
	auth.PermSubscribersGetAll,
	auth.PermCampaignsGetAll,
	auth.PermCampaignsManageAll,
	auth.PermCampaignsGetAnalytics,
}
