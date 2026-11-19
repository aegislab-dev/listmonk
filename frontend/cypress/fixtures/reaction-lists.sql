-- Synthetic browser fixture. Record accepted sends without invoking the mail engine.
DO $$
DECLARE
    source_id INT;
    campaign INT;
    offer INT;
    opened INT;
    clicked INT;
BEGIN
    INSERT INTO lists(uuid, name, type)
    VALUES (gen_random_uuid(), 'Cypress reaction audience', 'private') RETURNING id INTO source_id;

    INSERT INTO campaigns(uuid, name, subject, from_email, body, status, messenger, to_send, sent, started_at)
    VALUES (gen_random_uuid(), 'Cypress reaction recipients', 'Synthetic engagement fixture', 'sender@example.invalid',
        '<p>Fixture</p>', 'finished', 'email', 2, 2, NOW()) RETURNING id INTO campaign;
    INSERT INTO campaign_lists(campaign_id, list_id, list_name)
    VALUES (campaign, source_id, 'Cypress reaction audience');

    INSERT INTO subscribers(uuid, name, email)
    VALUES (gen_random_uuid(), 'Alice Example', 'alice-reaction@example.invalid') RETURNING id INTO opened;
    INSERT INTO subscribers(uuid, name, email)
    VALUES (gen_random_uuid(), 'Brenda Example', 'brenda-reaction@example.invalid') RETURNING id INTO clicked;
    INSERT INTO subscriber_lists(subscriber_id, list_id, status)
    VALUES (opened, source_id, 'confirmed'), (clicked, source_id, 'confirmed');
    INSERT INTO axm_campaign_sends(campaign_id, subscriber_id)
    VALUES (campaign, opened), (campaign, clicked);
    INSERT INTO campaign_views(campaign_id, subscriber_id)
    VALUES (campaign, opened), (campaign, clicked);
    INSERT INTO links(uuid, url) VALUES (gen_random_uuid(), 'https://example.invalid/cypress-reaction') RETURNING id INTO offer;
    INSERT INTO link_clicks(campaign_id, link_id, subscriber_id) VALUES (campaign, offer, clicked);
END $$;

UPDATE settings SET value = 'true'::JSONB WHERE key = 'privacy.individual_tracking';
