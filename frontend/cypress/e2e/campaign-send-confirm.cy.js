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
      create('unsched', { lists: [listX], send_at: new Date(Date.now() + 86400000 * 25).toISOString() });
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
    ready();
    // The title says that it changed (approved design A7); the counts say what changed, in place.
    cy.get('[data-cy=send-title]').should('have.text', 'The campaign changed in the meantime');
    cy.get('[data-cy=send-sub]').should('contain', 'loaded again');
    cy.get(`${win} .notification`).should('not.exist');
    cy.get('[data-cy=send-recipients]').should('contain', '4 in all');
    cy.get('[data-cy=send-recipients-was]').should('have.text', 'before: 3');
    cy.get('[data-cy=send-subject]').should('contain', 'Send stale changed');
    cy.get('[data-cy=send-ok]').should('contain', 'Send to 4 people');
    cy.request(`/api/campaigns/${camp.stale}`).its('body.data.status').should('eq', 'draft');

    // Confirming what is now shown goes through.
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').its('response.statusCode').should('eq', 200);
    cy.get(win).should('not.exist');
  });

  it('asks for what the campaign is now when the schedule was taken off under the window', () => {
    cy.visit('/admin/campaigns');
    cy.intercept('PUT', statusPath).as('status');
    row('unsched').find('[data-cy=btn-schedule]').click();
    ready();
    cy.get(`${win}[data-mode=schedule]`).should('exist');
    cy.request('PUT', `/api/campaigns/${camp.unsched}`, {
      name: `Send unsched ${stamp}`, subject: 'Send unsched subject', type: 'regular', messenger: 'email', lists: [listX],
      content_type: 'richtext', body: '<p>Hello <a href="{{ UnsubscribeURL }}">leave</a></p>', send_at: null,
    });
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').its('response.statusCode').should('eq', 409);
    ready();
    cy.get(`${win}[data-mode=start]`).should('exist');
    // The title says that it changed (A7); the time row and the button say what it does now.
    cy.get('[data-cy=send-title]').should('have.text', 'The campaign changed in the meantime');
    cy.get('[data-cy=send-when]').should('contain', 'right away');
    cy.get('[data-cy=send-ok]').should('contain', 'Send to');
    // The second confirmation is a start, not the schedule that no longer exists.
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').then(({ request, response }) => {
      expect(request.body.status).to.equal('running');
      expect(response.statusCode).to.equal(200);
    });
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
    // The box keeps its heading (approved design A6), with the reason and a retry button.
    cy.get('[data-cy=send-error] [data-cy=send-checks] .send-checks-head').should('have.text', 'Checks before sending');
    cy.get('[data-cy=send-error] [data-cy=send-checks]').should('contain', 'Could not get the number of recipients');
    cy.get('[data-cy=send-error-reason]').should('have.text', 'boom');
    cy.get('[data-cy=send-retry]').should('match', 'button');
    cy.get('[data-cy=send-ok]').should('contain', 'Go on without the count').click();
    // Without what was read, nothing is compared.
    cy.wait('@status').its('request.body').should('deep.equal', { status: 'running' });
  });

  it('reads the checks again with the retry button', () => {
    cy.visit('/admin/campaigns');
    cy.intercept('GET', preflight, { statusCode: 503, body: { message: 'Service unavailable' } }).as('pfDown');
    row('resume').find('[data-cy=btn-start]').click();
    cy.wait('@pfDown');
    cy.get(`${win}[data-state=error]`).should('exist');
    cy.intercept('GET', preflight, (req) => { req.continue(); }).as('pfUp');
    cy.get('[data-cy=send-retry]').click();
    cy.wait('@pfUp');
    ready();
    cy.get('[data-cy=send-title]').should('contain', 'people now');
    cy.get('[data-cy=send-ok]').should('not.be.disabled');
    cy.get('[data-cy=send-cancel]').click();
    cy.get(win).should('not.exist');
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

  it('says that the send time passed while the window was open, and does not send then', () => {
    cy.intercept('PUT', statusPath).as('status');
    cy.then(() => create('soon', { lists: [listX], send_at: new Date(Date.now() + 8000).toISOString() }));
    cy.visit('/admin/campaigns');
    row('soon').find('[data-cy=btn-schedule]').click();
    ready();
    cy.get('[data-cy="send-check-schedule.past"]').should('not.exist');
    cy.get('[data-cy=send-schedule-until]').should('be.visible');
    // The time comes while the window is open: the same warning as a fresh read gives.
    cy.get('[data-cy="send-check-schedule.past"]', { timeout: 15000 }).should('have.attr', 'data-level', 'warn');
    cy.get('[data-cy=send-schedule-until]').should('not.exist');
    cy.get('[data-cy=send-ok]').should('contain', 'anyway');
    cy.then(() => cy.request(`/api/campaigns/${camp.soon}/preflight`)).its('body.data.checks')
      .should((checks) => { expect(checks.map((c) => `${c.key}=${c.level}`)).to.include('schedule.past=warn'); });
    cy.get('@status.all').should('have.length', 0);
    cy.get('[data-cy=send-cancel]').click();
    cy.get(win).should('not.exist');
  });

  it('keeps time by the server clock when the browser clock is behind', () => {
    // The server says it is 2 minutes later than this browser thinks: its send time 60 s
    // from the browser's now has passed already.
    cy.intercept('GET', preflight, (req) => {
      req.continue((res) => { res.body.data.now = new Date(Date.now() + 120000).toISOString(); });
    }).as('pfAhead');
    cy.intercept('PUT', statusPath, { statusCode: 200, body: { data: { status: 'scheduled' } } }).as('status');
    cy.then(() => create('skew', { lists: [listX], send_at: new Date(Date.now() + 60000).toISOString() }));
    cy.visit('/admin/campaigns');
    row('skew').find('[data-cy=btn-schedule]').click();
    ready();
    cy.get('[data-cy="send-check-schedule.past"]').should('have.attr', 'data-level', 'warn');
    cy.get('[data-cy=send-schedule-until]').should('not.exist');
    cy.get('[data-cy=send-ok]').should('contain', 'anyway').click();
    // What the window showed goes with the request.
    cy.wait('@status').its('request.body.send_at_passed').should('eq', true);
  });

  it('reads again when the server refuses a passed send time the window did not show', () => {
    // The server's clock reads 2 minutes early here: the window believes there is time left.
    cy.intercept('GET', preflight, (req) => {
      req.continue((res) => { res.body.data.now = new Date(Date.now() - 120000).toISOString(); });
    }).as('pfBehind');
    cy.intercept('PUT', statusPath).as('status');
    cy.then(() => create('late', { lists: [listX], send_at: new Date(Date.now() + 4000).toISOString() }));
    cy.visit('/admin/campaigns');
    row('late').find('[data-cy=btn-schedule]').click();
    ready();
    cy.get('[data-cy=send-schedule-until]').should('be.visible');
    cy.wait(5000);
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').its('response.statusCode').should('eq', 412);
    ready();
    // Read again: the server's own check says the time has passed; nothing was sent.
    cy.get('[data-cy="send-check-schedule.past"]').should('have.attr', 'data-level', 'warn');
    cy.then(() => cy.request(`/api/campaigns/${camp.late}`)).its('body.data.status').should('eq', 'draft');
    cy.get('[data-cy=send-ok]').should('contain', 'anyway').click();
    cy.wait('@status').then(({ request, response }) => {
      expect(request.body.send_at_passed).to.equal(true);
      expect(response.statusCode).to.equal(200);
    });
    cy.get(win).should('not.exist');
  });

  it('keeps the window while the status change is on its way, then follows it', () => {
    cy.then(() => create('busy', { lists: [listX], send_at: new Date(Date.now() + 86400000 * 30).toISOString() }));
    cy.then(() => cy.visit(`/admin/campaigns/${camp.busy}`));
    cy.get('[data-cy=btn-schedule]').click();
    ready();
    cy.intercept('PUT', statusPath, (req) => { req.on('response', (res) => { res.setDelay(6000); }); }).as('slowStatus');
    cy.get('[data-cy=send-ok]').click();
    // Esc, the cancel button and a click outside do not close it.
    cy.get('body').type('{esc}');
    cy.get('[data-cy=send-cancel]').should('be.disabled');
    cy.get('.modal.is-active .modal-background').click({ force: true });
    // Still open (the page's loading cover is over it while the request is on its way).
    cy.get(win).should('exist');
    cy.get('[data-cy=send-ok]').should('have.class', 'is-loading');
    cy.wait('@slowStatus').its('response.statusCode').should('eq', 200);
    // The answer arrives: the page goes to the list, as after any start.
    cy.location('pathname').should('eq', '/admin/campaigns');
    cy.then(() => cy.request(`/api/campaigns/${camp.busy}`)).its('body.data.status').should('eq', 'scheduled');
  });

  it('shows only the campaign and its recipients when only the count changed', () => {
    let listC;
    cy.request('POST', '/api/lists', {
      name: `Send C ${stamp}`, type: 'private', optin: 'single', no_team: true,
    }).then(({ body }) => { listC = body.data.id; });
    cy.then(() => {
      person('c1', [listC]);
      create('count', { lists: [listC] });
    });
    cy.visit('/admin/campaigns');
    cy.intercept('PUT', statusPath).as('status');
    row('count').find('[data-cy=btn-start]').click();
    ready();
    cy.get('[data-cy=send-title]').should('contain', 'Send to 1 people now');
    // Somebody joins the list and the campaign is saved again as it was.
    cy.then(() => {
      person('c2', [listC]);
      cy.request(`/api/campaigns/${camp.count}`).then(({ body }) => {
        const c = body.data;
        cy.request('PUT', `/api/campaigns/${camp.count}`, {
          name: c.name, subject: c.subject, type: 'regular', messenger: 'email', lists: [listC], content_type: c.content_type, body: c.body,
        });
      });
    });
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').its('response.statusCode').should('eq', 409);
    ready();
    cy.get('[data-cy=send-title]').should('have.text', 'The campaign changed in the meantime');
    cy.get('[data-cy=send-campaign]').should('be.visible');
    cy.get('[data-cy=send-list-was]').should('contain', 'before: 1');
    cy.get('[data-cy=send-subject]').should('not.exist');
    cy.get('[data-cy=send-from]').should('not.exist');
    cy.get('[data-cy=send-when]').should('not.exist');
    cy.get('[data-cy=send-ok]').should('contain', 'Send to 2 people');
    cy.get('[data-cy=send-cancel]').click();
  });
});
