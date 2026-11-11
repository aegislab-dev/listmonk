// Run against an already running disposable server. No resetDB or SMTP sends.
describe('Unverified reply addresses on dated drafts', () => {
  const future = '2099-01-01T00:00:00Z';
  const reply = `sender-draft-${Date.now()}@example.invalid`;
  let domains;
  let listId;
  let draft;
  const campaigns = [];
  const fields = () => ({
    name: 'Synthetic sender draft', subject: 'Synthetic sender draft',
    lists: [listId], messenger: 'email', content_type: 'html',
    body: '<p>Synthetic draft only</p>', template_id: 0, reply_to: reply,
  });
  const createDraft = () => cy.request('POST', '/api/campaigns', fields()).then(({ body }) => {
    campaigns.push(body.data.id);
    return body.data;
  });
  const rejected = (method, url, body) => cy.request({ method, url, body, failOnStatusCode: false })
    .then((response) => {
      expect(response.status).to.eq(400);
      expect(response.body.message).to.include(reply).and.match(/verified|인증/);
    });

  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.location('pathname').should('eq', '/admin/campaigns');
    cy.request('/api/settings').then(({ body }) => { domains = body.data['axm.sender_domains']; });
    cy.request('PUT', '/api/settings/axm.sender_domains', ['crevasse.ai']);
    cy.waitForBackend();
    cy.request('POST', '/api/lists', {
      name: `Synthetic sender draft ${Date.now()}`, type: 'private', optin: 'single', tags: [],
    }).then(({ body }) => { listId = body.data.id; });
  });

  after(() => {
    campaigns.forEach((id) => {
      cy.request({ method: 'PUT', url: `/api/campaigns/${id}/status`, body: { status: 'draft' }, failOnStatusCode: false });
      cy.request('DELETE', `/api/campaigns?id=${id}`);
    });
    if (listId) cy.request('DELETE', `/api/lists?id=${listId}`);
    if (domains) {
      cy.request('PUT', '/api/settings/axm.sender_domains', domains);
      cy.waitForBackend();
    }
  });

  it('persists dates on an unverified draft without activating it, including a spoofed status', () => {
    createDraft().then((campaign) => {
      draft = campaign;
      cy.request('PUT', `/api/campaigns/${draft.id}`, {
        lists: [listId], send_at: future, status: 'scheduled',
      }).then(({ body }) => {
        expect(body.data.status).to.eq('draft');
        expect(body.data.reply_to_verified).to.eq(false);
        expect(Date.parse(body.data.send_at)).to.eq(Date.parse(future));
      });
      cy.request(`/api/campaigns/${draft.id}`).then(({ body }) => {
        expect(body.data.status).to.eq('draft');
        expect(Date.parse(body.data.send_at)).to.eq(Date.parse(future));
      });
      ['running', 'scheduled'].forEach((status) => rejected('PUT', `/api/campaigns/${draft.id}/status`, { status }));
      rejected('POST', `/api/campaigns/${draft.id}/test`, { ...fields(), subscribers: ['absent@example.invalid'] });
      cy.request('PUT', `/api/campaigns/${draft.id}`, { lists: [listId], send_at: null })
        .its('body.data').should('include', { status: 'draft', send_at: null });
    });
  });

  it('still rejects an unverified reply on scheduled edits, with or without a date', () => {
    createDraft().then((campaign) => {
      cy.request('PUT', `/api/campaigns/${campaign.id}`, {
        lists: [listId], send_at: future, reply_to: 'synthetic@crevasse.ai',
      });
      cy.request('PUT', `/api/campaigns/${campaign.id}/status`, { status: 'scheduled' })
        .its('body.data.status').should('eq', 'scheduled');
      [future, null].forEach((sendAt) => rejected('PUT', `/api/campaigns/${campaign.id}`, {
        lists: [listId], reply_to: reply, send_at: sendAt, status: 'draft',
      }));
      cy.request(`/api/campaigns/${campaign.id}`).its('body.data')
        .should('include', { status: 'scheduled', reply_to: 'synthetic@crevasse.ai' });
    });
  });

  it('saves a date from the editor while the reply remains unverified', () => {
    cy.viewport(1440, 1000);
    cy.visit(`/admin/campaigns/${draft.id}`);
    cy.get('input[name=reply_to]').should('have.value', reply);
    cy.get('[data-cy=btn-send-later] .check').click();
    cy.get('.datepicker input').click();
    cy.get('.datepicker-header .control:nth-child(2) select').select(String(new Date().getFullYear() + 1));
    cy.get('.datepicker-body a.is-selectable').first().click();
    cy.get('body').click(1, 1);
    cy.intercept('PUT', `/api/campaigns/${draft.id}`).as('saveDatedDraft');
    cy.get('[data-cy=btn-save]').click();
    cy.wait('@saveDatedDraft').then(({ response }) => {
      expect(response.statusCode).to.eq(200);
      expect(response.body.data.status).to.eq('draft');
      expect(response.body.data.send_at).not.to.eq(null);
      expect(response.body.data.reply_to_verified).to.eq(false);
    });
    cy.get('.notification[role=alert]').should('not.exist');
    cy.get('[data-cy=btn-schedule]').click();
    cy.get('.notification[role=alert]').filter(':visible').should('contain', reply);
    cy.get('.modal.is-active').should('not.exist');
  });

  it('keeps an unverified error on refresh, then clears it when the refreshed address is verified', () => {
    cy.get('.sender-address-input button.is-text').click();
    cy.get('.notification[role=alert]').filter(':visible').should('contain', reply);
    // Model the server response after ownership confirmation, without sending mail.
    cy.intercept('GET', '/api/sender-addresses', { body: { data: [{
      id: 1, email: reply, status: 'verified', verified_at: new Date().toISOString(),
    }] } }).as('verifiedAddresses');
    cy.get('.sender-address-input button.is-text').click();
    cy.wait('@verifiedAddresses');
    cy.get('.sender-address-input .tag').should('contain', '✓');
    cy.get('.notification[role=alert]').should('not.exist');
  });
});
