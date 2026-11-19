export const RECENT_DAYS = Object.freeze([7, 14, 30]);
export const RECENT_SCOPES = Object.freeze(['all', 'team', 'campaigns']);
export const RECENT_MAX_CAMPAIGNS = 20;

export const defaultRecentRule = () => ({
  enabled: false, days: 14, scope: 'all', campaignIds: [],
});

export const recentRulePayload = (rule) => {
  if (!rule.enabled) return { enabled: false };
  return {
    enabled: true,
    days: rule.days,
    scope: rule.scope,
    campaign_ids: rule.scope === 'campaigns' ? [...rule.campaignIds].sort((a, b) => a - b) : [],
  };
};
