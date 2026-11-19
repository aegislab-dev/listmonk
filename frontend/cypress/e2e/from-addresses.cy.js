// Run against an already running disposable server. No resetDB or SMTP sends.
describe('Team From address list', () => {
  const stamp = Date.now();
  const mine = 'aegislab_us@crevasse.ai';
  const team = 'AEGIS 팀 <team@crevasse.ai>';
  let listId;
  let saved;
  const campaigns = [];
  const fields = (from, name) => ({
    name: name || `Synthetic from list ${stamp}`, subject: 'Synthetic from list', lists: [listId],
    messenger: 'email', content_type: 'html', body: '<p>Synthetic draft only</p>', template_id: 0,
    ...(from === undefined ? {} : { from_email: from }),
  });
  const create = (from, name) => cy.request({ method: 'POST', url: '/api/campaigns', body: fields(from, name), failOnStatusCode: false })
    .then((r) => {
      if (r.status === 200) campaigns.push(r.body.data.id);
      return r;
    });
  const setList = (list) => {
    cy.request('PUT', '/api/settings/axm.from_addresses', list);
    cy.waitForBackend();
  };
  const refused = (r, text) => {
    expect(r.status).to.eq(400);
    if (text) expect(r.body.message).to.include(text);
  };

  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.location('pathname').should('eq', '/admin/campaigns');
    cy.request('/api/settings').then(({ body }) => { saved = body.data['axm.from_addresses'] || []; });
    cy.request('POST', '/api/lists', {
      name: `Synthetic from list ${stamp}`, type: 'private', optin: 'single', tags: [],
    }).then(({ body }) => { listId = body.data.id; });
    setList([]);
  });

  after(() => {
    campaigns.forEach((id) => {
      cy.request({ method: 'PUT', url: `/api/campaigns/${id}/status`, body: { status: 'draft' }, failOnStatusCode: false });
      cy.request({ method: 'DELETE', url: `/api/campaigns?id=${id}`, failOnStatusCode: false });
    });
    if (listId) cy.request('DELETE', `/api/lists?id=${listId}`);
    if (saved) setList(saved);
  });

  let oldDraft;
  let cloneDraft;

  it('with an empty list: input stays, spaces are trimmed, placeholders are refused', () => {
    create('  old@crevasse.ai ').then((r) => {
      expect(r.status).to.eq(200);
      expect(r.body.data.from_email).to.eq('old@crevasse.ai');
      oldDraft = r.body.data;
    });
    create('change-me@crevasse.ai').then((r) => refused(r, 'change-me@crevasse.ai'));
    create('Team <a@example.com>').then((r) => refused(r));
    create('x@crevasse.ai\r\nBcc: y@crevasse.ai').then((r) => refused(r));
    create('clone@crevasse.ai', `Synthetic clone source ${stamp}`).then((r) => { cloneDraft = r.body.data; });
    cy.then(() => cy.request({ method: 'PUT', url: `/api/campaigns/${oldDraft.id}`, body: fields('change-me@crevasse.ai'), failOnStatusCode: false }))
      .then((r) => refused(r, 'change-me@crevasse.ai'));
    cy.visit('/admin/campaigns/new');
    cy.get('input[name=from_email]').should('exist');
    cy.get('select[name=from_email]').should('not.exist');
  });

  it('refuses placeholders in the settings list', () => {
    ['change-me@crevasse.ai', 'a@example.com', 'noreply@listmonk.yoursite.com', 'not an address'].forEach((v) => {
      cy.request({ method: 'PUT', url: '/api/settings/axm.from_addresses', body: [mine, v], failOnStatusCode: false })
        .then((r) => expect(r.status).to.eq(400));
    });
    cy.request('/api/settings').then(({ body }) => expect(body.data['axm.from_addresses']).to.deep.eq([]));
  });

  it('with a list: API refuses other addresses and writes the list spelling', () => {
    setList([` ${mine.toUpperCase()} `, team]);
    cy.request('/api/settings').then(({ body }) => expect(body.data['axm.from_addresses']).to.deep.eq([mine, team]));

    create('other@crevasse.ai').then((r) => refused(r, 'other@crevasse.ai'));
    create('change-me@crevasse.ai').then((r) => refused(r, 'change-me@crevasse.ai'));
    create(` ${mine}`).then((r) => expect(r.body.data.from_email).to.eq(mine));
    create('Someone <team@crevasse.ai>').then((r) => expect(r.body.data.from_email).to.eq(team));
    create().then((r) => expect(r.body.data.from_email).to.eq(mine));
    cy.then(() => cy.request({ method: 'PUT', url: `/api/campaigns/${oldDraft.id}`, body: fields('other@crevasse.ai'), failOnStatusCode: false }))
      .then((r) => refused(r, 'other@crevasse.ai'));
  });

  it('shows a select with the default for a new campaign', () => {
    cy.visit('/admin/campaigns/new');
    cy.get('input[name=from_email]').should('not.exist');
    cy.get('[data-cy=from-warning]').should('not.exist');
    cy.get('select[name=from_email] option').then(($o) => {
      expect([...$o].map((o) => o.value).filter(Boolean)).to.deep.eq([mine, team]);
    });
    cy.get('select[name=from_email]').find('option:selected').should('have.value', mine);
  });

  it('warns on an old draft that is not on the list, leaves it untouched, and blocks starting it', () => {
    const status = (st) => cy.request({ method: 'PUT', url: `/api/campaigns/${oldDraft.id}/status`, body: { status: st }, failOnStatusCode: false });
    cy.then(() => cy.request(`/api/campaigns/${oldDraft.id}`)).then(({ body }) => expect(body.data.from_email).to.eq('old@crevasse.ai'));
    cy.then(() => status('running')).then((r) => refused(r, 'old@crevasse.ai'));
    cy.then(() => status('scheduled')).then((r) => refused(r, 'old@crevasse.ai'));
    cy.then(() => cy.request(`/api/campaigns/${oldDraft.id}`)).then(({ body }) => {
      expect(body.data.status).to.eq('draft');
      expect(body.data.from_email).to.eq('old@crevasse.ai');
    });

    cy.then(() => cy.visit(`/admin/campaigns/${oldDraft.id}`));
    cy.get('[data-cy=from-warning]').should('be.visible').and('contain', 'old@crevasse.ai');
    cy.get('select[name=from_email]').should(($s) => expect($s.val() || '').to.eq(''));
    // Opening it changed nothing.
    cy.then(() => cy.request(`/api/campaigns/${oldDraft.id}`)).then(({ body }) => expect(body.data.from_email).to.eq('old@crevasse.ai'));

    cy.get('select[name=from_email]').select(team);
    cy.get('[data-cy=btn-save]').first().click();
    cy.get('[data-cy=from-warning]').should('not.exist');
    cy.then(() => cy.request(`/api/campaigns/${oldDraft.id}`)).then(({ body }) => expect(body.data.from_email).to.eq(team));
  });

  it('copies a draft whose From is not on the list with the default From', () => {
    cy.then(() => cy.request(`/api/campaigns/${cloneDraft.id}`)).then(({ body }) => expect(body.data.from_email).to.eq('clone@crevasse.ai'));
    cy.loginAndVisit('/admin/campaigns');
    cy.get('input[name=query]').clear().type(`Synthetic clone source ${stamp}{enter}`);
    cy.get('tbody tr').should('have.length', 1);
    cy.get('[data-cy=btn-clone]').click();
    cy.get('.modal input').clear().type(`Synthetic from list copy ${stamp}`);
    cy.get('.modal button.is-primary:eq(0)').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cy.request(`/api/campaigns?query=${encodeURIComponent(`Synthetic from list copy ${stamp}`)}&no_body=true`).then(({ body }) => {
      expect(body.data.results).to.have.length(1);
      campaigns.push(body.data.results[0].id);
      expect(body.data.results[0].from_email).to.eq(mine);
    });
  });

  it('goes back to free input when the list is emptied', () => {
    setList([]);
    cy.visit('/admin/campaigns/new');
    cy.get('input[name=from_email]').should('exist');
    cy.get('select[name=from_email]').should('not.exist');
  });
});
