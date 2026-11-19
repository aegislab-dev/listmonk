// Run against an already running disposable server. No resetDB or SMTP sends.
describe('Team From address list', () => {
  const stamp = Date.now();
  const mine = 'aegislab_us@crevasse.ai';
  const team = 'AEGIS 팀 <team@crevasse.ai>';
  let listId;
  let saved;
  let savedSettings;
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
  // A string value needs an explicit JSON body.
  const setKey = (key, value) => cy.request({
    method: 'PUT', url: `/api/settings/${key}`, body: JSON.stringify(value), headers: { 'Content-Type': 'application/json' },
  });
  const refused = (r, text) => {
    expect(r.status).to.eq(400);
    if (text) expect(r.body.message).to.include(text);
  };

  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.location('pathname').should('eq', '/admin/campaigns');
    cy.request('/api/settings').then(({ body }) => {
      saved = body.data['axm.from_addresses'] || [];
      savedSettings = {
        'app.from_email': body.data['app.from_email'],
        'axm.sender_domains': body.data['axm.sender_domains'],
        'axm.default_from': body.data['axm.default_from'],
      };
    });
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
    if (savedSettings) {
      cy.request('PUT', '/api/settings/axm.sender_domains', savedSettings['axm.sender_domains']);
      cy.waitForBackend();
      setKey('axm.default_from', savedSettings['axm.default_from']);
      cy.waitForBackend();
      setKey('app.from_email', savedSettings['app.from_email']);
      cy.waitForBackend();
    }
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
    ['change-me@crevasse.ai', 'a@example.com', 'noreply@listmonk.yoursite.com', 'not an address', '"News Team"@example.org', '"change-me"@crevasse.ai'].forEach((v) => {
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

  it('applies the same checks to the app default From when none is sent', () => {
    setList([]);
    setKey('app.from_email', 'change-me@crevasse.ai');
    cy.waitForBackend();
    create().then((r) => refused(r, 'change-me@crevasse.ai'));
    setKey('app.from_email', ' Team <team@crevasse.ai> ');
    cy.waitForBackend();
    create().then((r) => expect(r.body.data.from_email).to.eq('Team <team@crevasse.ai>'));
    setKey('app.from_email', savedSettings['app.from_email']);
    cy.waitForBackend();
  });

  it('keeps quotes in a local part and refuses a custom From header while a list is in use', () => {
    setList(['"two words"@crevasse.ai', mine]);
    cy.request('/api/settings').then(({ body }) => expect(body.data['axm.from_addresses']).to.deep.eq(['"two words"@crevasse.ai', mine]));
    create('"two words"@crevasse.ai').then((r) => expect(r.body.data.from_email).to.eq('"two words"@crevasse.ai'));
    cy.request({
      method: 'POST', url: '/api/campaigns', failOnStatusCode: false,
      body: { ...fields(mine), headers: [{ From: 'other@crevasse.ai' }] },
    }).then((r) => refused(r));
    cy.request({
      method: 'PUT', url: '/api/settings/axm.from_addresses', failOnStatusCode: false, body: [`${mine}\n`],
    }).then((r) => expect(r.status).to.eq(400));
  });

  it('writes the list spelling when a draft with another spelling is scheduled', () => {
    setList([]);
    let id;
    cy.request({
      method: 'POST', url: '/api/campaigns',
      body: { ...fields(' Legacy Team <TEAM@crevasse.ai> '), send_at: '2099-01-01T00:00:00Z' },
    }).then((r) => {
      id = r.body.data.id;
      campaigns.push(id);
      expect(r.body.data.from_email).to.eq('Legacy Team <TEAM@crevasse.ai>');
    });
    setList(['Approved Team <team@crevasse.ai>']);
    cy.then(() => cy.request('PUT', `/api/campaigns/${id}/status`, { status: 'scheduled' }));
    cy.then(() => cy.request(`/api/campaigns/${id}`)).then(({ body }) => {
      expect(body.data.status).to.eq('scheduled');
      expect(body.data.from_email).to.eq('Approved Team <team@crevasse.ai>');
    });
  });

  it('leaves account senders alone, and warns about a placeholder draft once the list is empty', () => {
    setList([]);
    cy.request('PUT', '/api/settings/axm.sender_domains', ['crevasse.ai']);
    cy.waitForBackend();
    setKey('axm.default_from', 'change-me@crevasse.ai');
    cy.waitForBackend();
    let id;
    create().then((r) => {
      expect(r.status).to.eq(200);
      id = r.body.data.id;
    });
    setKey('axm.default_from', savedSettings['axm.default_from']);
    cy.waitForBackend();
    cy.request('PUT', '/api/settings/axm.sender_domains', savedSettings['axm.sender_domains']);
    cy.waitForBackend();
    cy.then(() => cy.request(`/api/campaigns/${id}`)).then(({ body }) => expect(body.data.from_email).to.eq('change-me@crevasse.ai'));
    cy.then(() => cy.visit(`/admin/campaigns/${id}`));
    cy.get('[data-cy=from-warning]').should('be.visible').and('contain', 'change-me@crevasse.ai');
    cy.get('input[name=from_email]').should('have.value', 'change-me@crevasse.ai');
  });

  it('shows a list changed in another session on a page that was already open', () => {
    setList([mine]);
    cy.loginAndVisit('/admin/campaigns');
    cy.get('[data-cy=btn-new]').should('exist');
    cy.request('PUT', '/api/settings/axm.from_addresses', ['updated@crevasse.ai']);
    cy.waitForBackend();
    cy.get('[data-cy=btn-new]').click();
    cy.get('select[name=from_email]').find('option:selected').should('have.value', 'updated@crevasse.ai');
    cy.get('select[name=from_email] option').then(($o) => {
      expect([...$o].map((o) => o.value).filter(Boolean)).to.deep.eq(['updated@crevasse.ai']);
    });
  });

  it('does not carry a From from an old config into a new campaign', () => {
    setList([mine]);
    cy.loginAndVisit('/admin/campaigns');
    cy.get('[data-cy=btn-new]').should('exist');
    setList([]);
    cy.get('[data-cy=btn-new]').click();
    cy.get('input[name=from_email]').should('have.value', savedSettings['app.from_email'].trim());
  });

  it('keeps the format check for the default From with account senders on', () => {
    setList([]);
    cy.request('PUT', '/api/settings/axm.sender_domains', ['crevasse.ai']);
    cy.waitForBackend();
    setKey('app.from_email', 'Newsletter');
    cy.waitForBackend();
    create().then((r) => refused(r));
    setKey('app.from_email', savedSettings['app.from_email']);
    cy.waitForBackend();
    cy.request('PUT', '/api/settings/axm.sender_domains', savedSettings['axm.sender_domains']);
    cy.waitForBackend();
  });

  it('takes a comma inside quotes as one address in the settings list', () => {
    cy.visit('/admin/settings');
    cy.get('[data-cy=from-addresses] input').type('"news,team"@crevasse.ai{enter}');
    cy.get('[data-cy=from-addresses] .tag').should('have.length.at.least', 1)
      .then(($t) => expect([...$t].map((t) => t.textContent.trim()).filter((x) => x.includes('news,team'))).to.have.length(1));
    cy.get('[data-cy=from-addresses] .tag').should('not.contain', '"news');
  });

  it('goes back to free input when the list is emptied', () => {
    setList([]);
    cy.visit('/admin/campaigns/new');
    cy.get('input[name=from_email]').should('exist');
    cy.get('select[name=from_email]').should('not.exist');
  });
});
