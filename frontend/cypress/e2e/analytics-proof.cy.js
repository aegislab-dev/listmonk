// Proof summary copy on the campaign analytics screen (LST-36). The first part pins the text
// with fixed API answers (no data of the server is read); the second part runs against a
// disposable server with a real team member, no mail is sent.
const stamp = Date.now();
const UUID = '4d9b346a-2085-4d49-9257-fca74821f256';
const CREATED = '2026-11-12T01:12:00Z';
const NOW = new Date('2026-11-19T06:30:00Z').getTime(); // 2026-11-19 15:30 KST

const campaign = (over = {}) => ({
  id: 52,
  uuid: UUID,
  name: '북미 틱톡 어필리에이트',
  status: 'finished',
  content_type: 'richtext',
  messenger: 'email',
  created_at: CREATED,
  started_at: CREATED,
  sent: 15390,
  to_send: 15390,
  lists: [{ id: 9001, name: '시험 리스트' }],
  tags: [],
  ...over,
});

const rows = (n) => [{ campaign_id: 52, count: n, timestamp: '2026-11-13T00:00:00Z' }];

const golden = [
  '[listmonk 집계 v1]',
  '기준 시각: 2026-11-19 15:30 KST',
  '캠페인: #52 · 북미 틱톡 어필리에이트 (팀: 마케팅-2팀)',
  `캠페인 UUID: ${UUID}`,
  '발송 15,390 · 열람 1,664 (10.81%) · 클릭 27 (0.18%) · 반송 392 (2.55%)',
  '정의: 발송=listmonk 발송 카운터(수신함 도착이 아님) · 열람/클릭=고유 수신자 수 · 반송=반송 기록 행 수 · 비율의 분모=발송',
  '회신: 집계 전',
  '포함 안 함: 이름·이메일 주소·본문·개인별 활동',
  `확인 링크: https://lists.crevasse.ai/admin/campaigns/analytics?id=52&from=${Math.floor(new Date(CREATED).getTime() / 1000)}&to=${Math.floor(NOW / 1000)} (listmonk 권한 필요)`,
].join('\n');

const fixed = ({ privacy, camp, views = 1664, clicks = 27, bounces = 392 } = {}) => {
  cy.intercept('GET', '/api/config', (req) => {
    req.continue((res) => {
      res.body.data.root_url = 'https://lists.crevasse.ai';
      res.body.data.privacy = { ...res.body.data.privacy, disable_tracking: false, individual_tracking: true, ...privacy };
    });
  });
  cy.intercept('GET', '/api/lists*', { data: { results: [{ id: 9001, name: '시험 리스트', tags: ['팀:마케팅-2팀', '북미'] }], total: 1, per_page: 'all', page: 1 } });
  cy.intercept('GET', '/api/campaigns/52', { data: camp || campaign() }).as('camp');
  cy.intercept('GET', '/api/campaigns/analytics/views*', { data: rows(views) }).as('views');
  cy.intercept('GET', '/api/campaigns/analytics/clicks*', { data: rows(clicks) }).as('clicks');
  cy.intercept('GET', '/api/campaigns/analytics/bounces*', { data: rows(bounces) }).as('bounces');
  cy.intercept('GET', '/api/campaigns/analytics/links*', { data: [] });
};

const loginAs = (user, pass, next) => {
  cy.visit(`/admin/login?next=${next}`);
  cy.get('input[name=username]').invoke('val', user);
  cy.get('input[name=password]').invoke('val', pass);
  cy.get('button').click();
};

describe('proof summary (fixed answers)', () => {
  beforeEach(() => {
    cy.viewport(1440, 1000);
  });

  it('opens the block as a fixed text, over the whole life of the campaign, whatever dates are picked', () => {
    cy.loginAndVisit('/admin/campaigns/analytics');
    cy.clock(NOW, ['Date']);
    fixed();
    // The picked dates are one day long, long after the campaign was made.
    const day = Math.floor(new Date('2026-11-18T00:00:00Z').getTime() / 1000);
    cy.visit(`/admin/campaigns/analytics?id=52&from=${day}&to=${day + 3600}`);
    cy.get('[data-cy=btn-proof-summary]').should('not.be.disabled');
    // The screen loads its own charts for the picked dates; the summary asks again for the whole life.
    cy.wait('@views').its('request.url').then((url) => {
      expect(new URL(url).searchParams.get('from')).to.eq(new Date(day * 1000).toISOString());
    });
    cy.get('[data-cy=btn-proof-summary]').click();

    cy.wait('@views').its('request.url').then((url) => {
      const q = new URL(url).searchParams;
      expect(q.get('from')).to.eq(new Date(CREATED).toISOString());
      expect(q.get('to')).to.eq(new Date(NOW).toISOString());
    });
    cy.get('[data-cy=proof-summary-text]').invoke('val').then((text) => {
      expect(text).to.eq(golden);
      expect(text).not.to.contain('@');
      expect(text.split('\n')).to.have.length(9);
    });
  });

  it('copies the same text to the clipboard and says so', () => {
    cy.loginAndVisit('/admin/campaigns/analytics');
    cy.clock(NOW, ['Date']);
    fixed();
    cy.visit('/admin/campaigns/analytics?id=52');
    cy.window().then((win) => {
      cy.stub(win.navigator.clipboard, 'writeText').resolves().as('copy');
    });
    cy.get('[data-cy=btn-proof-summary]').click();
    cy.get('[data-cy=btn-proof-copy]').click();
    cy.get('@copy').should('have.been.calledOnceWith', golden);
    cy.get('.toast').should('contain', '·');
  });

  it('turns the button off for two campaigns and for a draft or scheduled one', () => {
    cy.loginAndVisit('/admin/campaigns/analytics');
    fixed({ camp: campaign({ status: 'draft' }) });
    cy.visit('/admin/campaigns/analytics?id=52');
    cy.get('[data-cy=btn-proof-summary]').should('be.disabled');
    cy.get('[data-cy=proof-summary-help]').should('not.be.empty');

    fixed({ camp: campaign({ status: 'scheduled' }) });
    cy.visit('/admin/campaigns/analytics?id=52');
    cy.get('[data-cy=btn-proof-summary]').should('be.disabled');

    fixed({ camp: campaign({ status: 'running' }) });
    cy.visit('/admin/campaigns/analytics?id=52');
    cy.get('[data-cy=btn-proof-summary]').should('not.be.disabled');
    cy.intercept('GET', '/api/campaigns/53', { data: campaign({ id: 53, uuid: '00000000-0000-4000-8000-000000000053' }) });
    cy.visit('/admin/campaigns/analytics?id=52&id=53');
    cy.get('[data-cy=btn-proof-summary]').should('be.disabled');
  });

  it('writes "추적 없음" when individual tracking is off, and when nothing was ever recorded', () => {
    cy.loginAndVisit('/admin/campaigns/analytics');
    cy.clock(NOW, ['Date']);
    fixed({ privacy: { individual_tracking: false } });
    cy.visit('/admin/campaigns/analytics?id=52');
    cy.get('[data-cy=btn-proof-summary]').click();
    cy.get('[data-cy=proof-summary-text]').invoke('val').then((text) => {
      expect(text.split('\n')[4]).to.eq('발송 15,390 · 열람 추적 없음 · 클릭 추적 없음 · 반송 392 (2.55%)');
    });

    fixed({ views: 0, clicks: 0, bounces: 0 });
    cy.visit('/admin/campaigns/analytics?id=52');
    cy.get('[data-cy=btn-proof-summary]').click();
    cy.get('[data-cy=proof-summary-text]').invoke('val').then((text) => {
      expect(text.split('\n')[4]).to.eq('발송 15,390 · 열람 추적 없음 · 클릭 추적 없음 · 반송 0 (0.00%)');
    });
  });
});

describe('proof summary (a team member, a real server)', () => {
  const teamA = `증빙A팀${stamp}`;
  const teamB = `증빙B팀${stamp}`;
  const user = `proof${stamp}`;
  const pass = 'proof-pass-1234';
  const made = { lists: [], campaigns: [], teams: [] };
  let roleId;
  let userId;
  let campA;
  let campB;

  const makeCampaign = (name, listId) => cy.request('POST', '/api/campaigns', {
    name, subject: name, lists: [listId], messenger: 'email', content_type: 'html', body: '<p>synthetic</p>', template_id: 0,
  }).then(({ body }) => {
    made.campaigns.push(body.data.id);
    // No subscribers on the list: starting it sends nothing, and it is no longer a draft.
    return cy.request('PUT', `/api/campaigns/${body.data.id}/status`, { status: 'running' })
      .then(() => cy.request(`/api/campaigns/${body.data.id}`)).then(({ body: b }) => b.data);
  });

  before(() => {
    cy.loginAndVisit('/admin/lists');
    cy.request('POST', '/api/roles/users', {
      name: `증빙 시험 ${stamp}`,
      permissions: ['campaigns:get', 'campaigns:get_analytics'],
    }).then(({ body }) => { roleId = body.data.id; });
    cy.then(() => cy.request('POST', '/api/teams', { name: teamA, user_role_id: roleId })).then(({ body }) => made.teams.push(body.data.id));
    cy.then(() => cy.request('POST', '/api/teams', { name: teamB, user_role_id: roleId })).then(({ body }) => made.teams.push(body.data.id));
    cy.then(() => cy.request('POST', '/api/users', {
      username: user, email: `${user}@example.invalid`, name: `증빙 ${stamp}`, type: 'user', status: 'enabled',
      password_login: true, password: pass, password2: pass, user_role_id: roleId,
    })).then(({ body }) => { userId = body.data.id; });
    cy.then(() => cy.request('PUT', `/api/teams/${made.teams[0]}/members`, { user_ids: [userId] }));
    cy.then(() => cy.request('POST', '/api/lists', { name: `증빙 A ${stamp}`, type: 'private', optin: 'single', tags: [`팀:${teamA}`] }))
      .then(({ body }) => made.lists.push(body.data.id));
    cy.then(() => cy.request('POST', '/api/lists', { name: `증빙 B ${stamp}`, type: 'private', optin: 'single', tags: [`팀:${teamB}`] }))
      .then(({ body }) => made.lists.push(body.data.id));
    cy.then(() => makeCampaign(`증빙 캠페인 A ${stamp}`, made.lists[0])).then((c) => { campA = c; });
    cy.then(() => makeCampaign(`증빙 캠페인 B ${stamp}`, made.lists[1])).then((c) => { campB = c; });
  });

  after(() => {
    cy.loginAndVisit('/admin/lists');
    made.campaigns.forEach((id) => {
      cy.request({ method: 'PUT', url: `/api/campaigns/${id}/status`, body: { status: 'cancelled' }, failOnStatusCode: false });
      cy.request({ method: 'DELETE', url: `/api/campaigns/${id}`, failOnStatusCode: false });
    });
    made.teams.forEach((id) => cy.request('PUT', `/api/teams/${id}/members`, { user_ids: [] }));
    cy.request({ method: 'DELETE', url: `/api/users/${userId}`, failOnStatusCode: false });
    made.lists.forEach((id) => cy.request({ method: 'DELETE', url: `/api/lists/${id}`, failOnStatusCode: false }));
    made.teams.forEach((id) => cy.request({ method: 'DELETE', url: `/api/teams/${id}`, failOnStatusCode: false }));
    cy.request({ method: 'DELETE', url: `/api/roles/${roleId}`, failOnStatusCode: false });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
  });

  it('gives a member the block of their own team campaign, with the team name', () => {
    // The login keeps no query string of the page it goes to: log in, then open the page.
    loginAs(user, pass, '/admin/campaigns/analytics');
    cy.get('[data-cy=btn-proof-summary]').should('exist');
    cy.visit(`/admin/campaigns/analytics?id=${campA.id}`);
    cy.get('[data-cy=btn-proof-summary]').should('not.be.disabled').click();
    cy.get('[data-cy=proof-summary-text]').invoke('val').then((text) => {
      const lines = text.split('\n');
      expect(lines).to.have.length(9);
      expect(lines[0]).to.eq('[listmonk 집계 v1]');
      expect(lines[2]).to.eq(`캠페인: #${campA.id} · 증빙 캠페인 A ${stamp} (팀: ${teamA})`);
      expect(lines[3]).to.eq(`캠페인 UUID: ${campA.uuid}`);
      expect(text).not.to.contain('@');
      expect(text).not.to.contain(teamB);
    });
  });

  it('refuses the other team campaign: the server says 403 and no block is made', () => {
    cy.request({ url: `/api/campaigns/analytics/views?id=${campB.id}&from=2020-01-01T00:00:00Z&to=2099-01-01T00:00:00Z`, failOnStatusCode: false })
      .its('status').should('eq', 403);
    loginAs(user, pass, '/admin/campaigns/analytics');
    cy.get('[data-cy=btn-proof-summary]').should('exist');
    cy.visit(`/admin/campaigns/analytics?id=${campB.id}`);
    cy.location('search').should('contain', `id=${campB.id}`);
    cy.get('[data-cy=btn-proof-summary]').should('be.disabled');
    cy.get('[data-cy=proof-summary-text]').should('not.exist');
  });
});
