// The window that every start, resume and schedule goes through (LST-48). Synthetic data
// only: two lists with an overlap, a list nobody is on, campaigns made through the API.
const stamp = Date.now();
const win = '[data-cy=send-confirm]';
const ready = () => cy.get(`${win}[data-state=ready]`);
const preflight = '/api/campaigns/*/preflight';
const statusPath = '/api/campaigns/*/status';

let listX;
let listY;
let listEmpty;
const camp = {};

const person = (n, lists, status = 'enabled') => cy.request('POST', '/api/subscribers', {
  email: `send-${n}-${stamp}@example.invalid`, name: `Send ${n}`, status, lists, preconfirm_subscriptions: true,
});

const create = (key, data) => cy.request('POST', '/api/campaigns', {
  name: `Send ${key} ${stamp}`,
  subject: `Send ${key} subject`,
  type: 'regular',
  messenger: 'email',
  content_type: 'richtext',
  body: '<p>Hello <a href="{{ UnsubscribeURL }}">leave</a></p>',
  ...data,
}).then(({ body }) => { camp[key] = body.data.id; });

const row = (key) => cy.contains('tbody tr', `Send ${key} ${stamp}`);

describe('Pre-send confirmation window', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    ['X', 'Y', 'Empty'].forEach((n) => {
      cy.request('POST', '/api/lists', {
        name: `Send ${n} ${stamp}`, type: 'private', optin: 'single', no_team: true,
      }).then(({ body }) => {
        if (n === 'X') listX = body.data.id;
        if (n === 'Y') listY = body.data.id;
        if (n === 'Empty') listEmpty = body.data.id;
      });
    });
    cy.then(() => {
      // X has 3 people, Y has 2, one is on both: 4 people in all.
      person(1, [listX]);
      person(2, [listX]);
      person(3, [listX, listY]);
      person(4, [listY]);
      // Blocked: on a list but never sent to, and said so in the window.
      person(5, [listX], 'blocklisted');
      create('page', { lists: [listX] });
      create('list', { lists: [listX, listY] });
      create('empty', { lists: [listEmpty] });
      create('later', { lists: [listX], send_at: new Date(Date.now() + 86400000 * 20).toISOString() });
      create('rule', { lists: [listX] });
      create('stale', { lists: [listX] });
      create('resume', { lists: [listX] });
      create('plain', { lists: [listX], content_type: 'plain', body: 'Plain text with no links {{ UnsubscribeURL }}' });
    });
    cy.then(() => {
      cy.request('PUT', `/api/campaigns/${camp.rule}/recent-exclude`, { enabled: true, days: 14, scope: 'all' });
    });
  });

  it('shows the lists as edited, not as saved, and sends to what it showed', () => {
    cy.visit(`/admin/campaigns/${camp.page}`);
    cy.get('.list-selector input').click().type(`Send Y ${stamp}`);
    cy.get('.list-selector .autocomplete a').contains(`Send Y ${stamp}`).click();
    cy.intercept('PUT', statusPath).as('status');
    cy.get('[data-cy=btn-start]').click();

    ready();
    cy.get('[data-cy=send-title]').should('contain', 'Send to 4 people now');
    cy.get('[data-cy=send-campaign]').should('contain', `Send page ${stamp}`);
    cy.get('[data-cy=send-subject]').should('contain', 'Send page subject');
    cy.get('[data-cy=send-from]').should('not.be.empty');
    cy.get(`[data-cy=send-list-${listX}]`).should('contain', `Send X ${stamp}`).and('contain', '3');
    cy.get(`[data-cy=send-list-${listY}]`).should('contain', `Send Y ${stamp}`).and('contain', '2');
    // 3 + 2 on the lists, one person is on both: counted once.
    cy.get('[data-cy=send-recipients]').should('contain', '1 on more than one list counted once, 4 in all')
      .and('contain', '1 blocked, unsubscribed or unconfirmed already left out');
    cy.get('[data-cy=send-check-unsubscribe\\.missing]').should('have.attr', 'data-level', 'ok');
    cy.get('[data-cy=send-ok]').should('contain', 'Send to 4 people').and('not.be.disabled');

    // Saved before the window opened: the campaign already has both lists.
    cy.request(`/api/campaigns/${camp.page}`).its('body.data.lists').should('have.length', 2);

    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').then(({ request }) => {
      expect(request.body.status).to.equal('running');
      // The time the window read, as the server gave it.
      expect(request.body.updated_at).to.be.a('string').and.match(/^\d{4}-\d{2}-\d{2}T/);
    });
    cy.location('pathname').should('match', /\/admin\/campaigns$/);
    cy.request(`/api/campaigns/${camp.page}`).then(({ body }) => {
      expect(['running', 'finished']).to.include(body.data.status);
      expect(body.data.lists.map((l) => l.id).sort()).to.deep.equal([listX, listY].sort());
    });
  });

  it('opens the same window from the list and leaves the campaign alone when cancelled', () => {
    cy.visit('/admin/campaigns');
    cy.intercept('PUT', statusPath).as('status');
    row('list').find('[data-cy=btn-start]').click();
    ready();
    cy.get('[data-cy=send-campaign]').should('contain', `Send list ${stamp}`);
    cy.get('[data-cy=send-title]').should('contain', 'Send to 4 people now');
    cy.get('[data-cy=send-recipients]').should('contain', '4 in all');
    cy.get('[data-cy=send-cancel]').click();
    cy.get(win).should('not.exist');
    cy.get('@status.all').should('have.length', 0);
    cy.request(`/api/campaigns/${camp.list}`).its('body.data.status').should('eq', 'draft');
  });

  it('cannot be confirmed when nobody would get the mail', () => {
    cy.visit('/admin/campaigns');
    row('empty').find('[data-cy=btn-start]').click();
    ready();
    cy.get('[data-cy=send-title]').should('contain', 'There is nobody to send to');
    cy.get('[data-cy="send-check-recipients.zero"]').should('have.attr', 'data-level', 'block');
    cy.get('[data-cy=send-ok]').should('be.disabled');
    // The summary says it, and the reason is the only line of the checks.
    cy.get('[data-cy=send-zero]').should('contain', 'Nobody can receive it');
    cy.get('[data-cy=send-checks] li').should('have.length', 1);
    cy.get('[data-cy=send-cancel]').click();
    cy.request(`/api/campaigns/${camp.empty}`).its('body.data.status').should('eq', 'draft');
  });

  it('shows the schedule with its time zone', () => {
    cy.visit('/admin/campaigns');
    row('later').find('[data-cy=btn-schedule]').click();
    ready();
    cy.get(`${win}[data-mode=schedule]`).should('exist');
    cy.get('[data-cy=send-title]').should('contain', 'Send to 3 people on');
    cy.get('[data-cy=send-schedule-time]').invoke('text').should('match', /^\d{4}-\d{2}-\d{2} \(.+\) \d{2}:\d{2}$/);
    cy.get('[data-cy=send-schedule-zone]').invoke('text').should('match', /\S+ · UTC([+-]\d+(:\d+)?)? ·/);
    cy.get('[data-cy=send-schedule-until]').should('contain', 'in about');
    cy.get('[data-cy=send-ok]').should('contain', 'Schedule for 3 people');
    cy.get('[data-cy=send-cancel]').click();
  });

  it('says in the window that a saved exclusion rule is not applied, from the list too', () => {
    cy.visit('/admin/campaigns');
    row('rule').find('[data-cy=btn-start]').click();
    ready();
    cy.get('[data-cy="send-check-recent.notApplied"]').should('have.attr', 'data-level', 'warn')
      .and('contain', 'does not apply to sending yet').and('contain', 'last 14 days');
    cy.get('[data-cy=send-sub]').should('contain', 'warning');
    cy.get('[data-cy=send-ok]').should('contain', 'anyway').and('not.be.disabled');
    cy.get('[data-cy=send-cancel]').click();
  });

  it('reads the campaign again when it changed while the window was open', () => {
    cy.visit('/admin/campaigns');
    cy.intercept('PUT', statusPath).as('status');
    row('stale').find('[data-cy=btn-start]').click();
    ready();
    cy.get('[data-cy=send-title]').should('contain', 'Send to 3 people now');

    // Somebody changes the campaign: another list, another subject.
    cy.request('PUT', `/api/campaigns/${camp.stale}`, {
      name: `Send stale ${stamp}`, subject: 'Send stale changed', type: 'regular', messenger: 'email', lists: [listX, listY],
      content_type: 'richtext', body: '<p>Hello <a href="{{ UnsubscribeURL }}">leave</a></p>',
    });
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').its('response.statusCode').should('eq', 409);
    cy.get('[data-cy=send-stale]').should('be.visible');
    cy.get('[data-cy=send-stale-count]').should('contain', 'from 3 to 4');
    ready();
    cy.get('[data-cy=send-subject]').should('contain', 'Send stale changed');
    cy.get('[data-cy=send-title]').should('contain', 'Send to 4 people now');
    cy.request(`/api/campaigns/${camp.stale}`).its('body.data.status').should('eq', 'draft');

    // Confirming what is now shown goes through.
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').its('response.statusCode').should('eq', 200);
    cy.get(win).should('not.exist');
  });

  it('shows a resumed campaign with who is left', () => {
    cy.visit('/admin/campaigns');
    // A paused campaign is not made here: the answer of the server is changed to one.
    cy.intercept('GET', preflight, (req) => {
      req.continue((res) => {
        const d = res.body.data;
        Object.assign(d, { mode: 'resume', status: 'paused', remaining: 12, sent: 180, to_send: 300 });
      });
    }).as('pf');
    row('resume').find('[data-cy=btn-start]').click();
    cy.wait('@pf');
    ready();
    cy.get(`${win}[data-mode=resume]`).should('exist');
    cy.get('[data-cy=send-title]').should('contain', 'Resume sending to about 12 remaining people');
    cy.get('[data-cy=send-progress]').should('contain', '180 sent of 300');
    cy.get('[data-cy=send-ok]').should('contain', 'Resume for about 12 people');
    cy.get('[data-cy=send-cancel]').click();
  });

  it('offers a retry and a way on when the checks cannot be read', () => {
    cy.visit('/admin/campaigns');
    cy.intercept('GET', preflight, { statusCode: 500, body: { message: 'boom' } }).as('pf');
    cy.intercept('PUT', statusPath, { statusCode: 200, body: { data: { status: 'running' } } }).as('status');
    row('resume').find('[data-cy=btn-start]').click();
    cy.wait('@pf');
    cy.get(`${win}[data-state=error]`).should('exist');
    // Which campaign it would go on for is still said.
    cy.get('[data-cy=send-error] [data-cy=send-campaign]').should('contain', `Send resume ${stamp}`);
    cy.get('[data-cy=send-error] [data-cy=send-subject]').should('contain', 'Send resume subject');
    cy.get('[data-cy=send-error]').should('contain', 'Could not get the number of recipients');
    cy.get('[data-cy=send-retry]').should('exist');
    cy.get('[data-cy=send-ok]').should('contain', 'Go on without the count').click();
    // Without what was read, nothing is compared.
    cy.wait('@status').its('request.body').should('deep.equal', { status: 'running' });
  });

  it('says which campaign it is while it reads, and does not call a plain text mail tracked', () => {
    cy.visit('/admin/campaigns');
    cy.intercept('GET', preflight, (req) => { req.on('response', (res) => { res.setDelay(1500); }); }).as('slowPf');
    row('plain').find('[data-cy=btn-start]').click();
    cy.get('[data-cy=send-loading] [data-cy=send-campaign]').should('contain', `Send plain ${stamp}`);
    cy.get('[data-cy=send-ok]').should('be.disabled');
    ready();
    cy.get('[data-cy=send-check-tracking]').should('have.length', 1).and('have.attr', 'data-level', 'info')
      .and('contain', 'plain text e-mail records no views');
    cy.get('[data-cy=send-checks]').should('not.contain', 'are tracked');
    cy.get('[data-cy=send-cancel]').click();
  });
});
