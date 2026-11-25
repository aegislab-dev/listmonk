export default ['finished', 'running', 'paused', 'cancelled'];

export function isFollowupSourceCandidate(campaign) {
  return campaign.status === 'finished' && campaign.type === 'regular';
}

export async function loadReactionPreset(api, campaignId) {
  try {
    const { role } = await api.getCampaignFollowup(campaignId);
    return role === 'none' || role === 'parent' ? 'followup' : '';
  } catch {
    // Keep the reaction list available if the follow-up lookup fails.
    return '';
  }
}
