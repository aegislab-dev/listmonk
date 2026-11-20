-- name: axm-campaign-for-send-check
-- A campaign as the send reads it (next-campaigns, LST-44): the row, the template body
-- (the default template's when the campaign has none or it was deleted) and the media
-- IDs. Not the lists: the send does not read them, so templates see none there.
-- $2: the template chosen on the page instead of the stored one; 0 for the stored one.
SELECT campaigns.*,
    COALESCE(templates.body, (SELECT body FROM templates WHERE is_default = true LIMIT 1), '') AS template_body,
    (SELECT ARRAY_AGG(campaign_media.media_id)::INT[] FROM campaign_media
        WHERE campaign_media.campaign_id = campaigns.id AND campaign_media.media_id IS NOT NULL) AS media_id
FROM campaigns
LEFT JOIN templates ON (templates.id = (CASE WHEN $2 = 0 THEN campaigns.template_id ELSE $2 END))
WHERE campaigns.id = $1;
