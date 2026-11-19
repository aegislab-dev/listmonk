// The pre-send tracking check (LST-44): the box on the campaign tab, the warning in the
// start confirmation and the test send notice.
const stamp = Date.now();
const check = '/api/campaigns/*/tracking-check';
// What the window every start goes through reads (LST-48): the tracking check is part of it.
const preflight = '/api/campaigns/*/preflight';
const box = '[data-cy=tracking-check]';
// The tracking warnings in the window every start goes through (LST-48).
// The window has read the checks. Without this a "not there" assertion passes while it is loading.
const ready = () => cy.get('[data-cy=send-confirm][data-state=ready]');
const warnLines = '[data-cy=send-confirm] [data-cy=send-check-tracking][data-level=warn]';

const barePixelless = '<!doctype html><html><body>{{ template "content" . }}'
  + '<p><a href="{{ UnsubscribeURL }}">Unsubscribe</a></p></body></html>';
const trackedBody = '<p><a href="https://example.invalid/offer@TrackLink">Offer</a></p>';
const mixedBody = `${trackedBody}<p><a href="https://example.invalid/guide">Guide</a>`
  + ' <a href="https://shop.example.invalid/item?id=7&amp;ref=mail">Item</a> <a href="mailto:help@example.invalid">Help</a></p>';

let listId;
let defaultTpl;
let bareTpl;
let pixelTpl;
const ids = {};

const create = (key, data) => cy.request('POST', '/api/campaigns', {
  name: `Tracking ${key} ${stamp}`,
  subject: `Tracking ${key}`,
  type: 'regular',
  messenger: 'email',
  // A real From: the stock one of a new install is a warning of its own in the window.
  from_email: 'Tracking <tracking@crevasse.ai>',
  lists: [listId],
  ...data,
}).then(({ body }) => { ids[key] = body.data.id; });

const open = (key) => {
  cy.intercept('POST', check).as('check');
  cy.visit(`/admin/campaigns/${ids[key]}`);
  cy.wait('@check');
  cy.get(box).should('not.have.attr', 'data-state', 'checking');
};

describe('Pre-send tracking check', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', {
      name: `Tracking list ${stamp}`, type: 'private', optin: 'single', no_team: true,
    }).then(({ body }) => { listId = body.data.id; });
    cy.request('/api/templates').then(({ body }) => {
      defaultTpl = body.data.find((t) => t.is_default).id;
    });
    cy.request('POST', '/api/templates', { name: `No pixel ${stamp}`, type: 'campaign', body: barePixelless })
      .then(({ body }) => { bareTpl = body.data.id; });
    cy.request('POST', '/api/templates', { name: `With pixel ${stamp}`, type: 'campaign', body: `${barePixelless}{{ TrackView }}` })
      .then(({ body }) => { pixelTpl = body.data.id; });
    // The window refuses to send to nobody: someone is on the list.
    cy.then(() => cy.request('POST', '/api/subscribers', {
      email: `tracking-${stamp}@example.invalid`, name: 'Tracking', status: 'enabled', lists: [listId], preconfirm_subscriptions: true,
    }));
    cy.then(() => {
      create('ok', { content_type: 'richtext', body: trackedBody, template_id: pixelTpl });
      create('warn', { content_type: 'richtext', body: mixedBody, template_id: bareTpl });
      create('plain', {
        content_type: 'plain', body: 'Notice https://example.invalid/notice\nOffer https://example.invalid/offer@TrackLink\n',
      });
      create('visual', {
        content_type: 'visual',
        body: '<html><body><a href="https://example.invalid/v">Visual link</a><a href="{{ UnsubscribeURL }}">Leave</a></body></html>',
      });
    });
  });

  it('is not shown before the campaign is saved', () => {
    cy.visit('/admin/campaigns/new');
    cy.get('input[name=name]').should('be.visible');
    cy.get(box).should('not.exist');
  });

  it('says ok for a template with the view pixel and tracked links', () => {
    open('ok');
    cy.get(box).should('have.attr', 'data-state', 'ok');
    cy.get('[data-cy=tracking-tag]').should('contain', 'OK');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-ok').and('contain', 'On');
    cy.get('[data-cy=tracking-click]').should('have.class', 'is-ok').and('contain', 'All 1 links tracked');
    cy.get('[data-cy=tracking-links-toggle]').should('not.exist');

    // Nothing to warn about: the confirmation is the usual one.
    cy.get('[data-cy=btn-start]').click();
    ready();
    cy.get(warnLines).should('not.exist');
    cy.get('[data-cy=send-ok]').should('contain', 'Send to').and('not.contain', 'anyway');
    cy.get('[data-cy=send-cancel]').click();
  });

  it('warns about a template without the pixel and lists the links that are not tracked', () => {
    open('warn');
    cy.get(box).should('have.attr', 'data-state', 'warn');
    cy.get('[data-cy=tracking-tag]').should('contain', 'Needs attention');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-warn').and('contain', 'Missing').and('contain', '{{ TrackView }}');
    cy.get('[data-cy=tracking-click]').should('have.class', 'is-warn').and('contain', '2 of 3 links not tracked')
      .and('contain', '@TrackLink');
    cy.get('[data-cy=tracking-links]').should('not.exist');
    cy.get('[data-cy=tracking-links-toggle]').click();
    cy.get('[data-cy=tracking-links]').children().then(($rows) => {
      expect([...$rows].map((r) => r.textContent.trim())).to.deep.equal(
        ['https://example.invalid/guide', 'https://shop.example.invalid/item?id=7&ref=mail'],
      );
    });
  });

  it('checks the unsaved editor state again when the template changes', () => {
    cy.intercept('POST', check).as('recheck');
    cy.get('.b-tabs nav a').eq(1).click();
    cy.get('select[name=template]').first().select(`${defaultTpl}`);
    cy.wait('@recheck').then(({ request, response }) => {
      expect(request.body.template_id).to.equal(defaultTpl);
      expect(request.body.content_type).to.equal('richtext');
      expect(response.body.data.view).to.equal('manual');
    });
    cy.get('.b-tabs nav a').eq(0).click();
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-ok').and('contain', 'On');
    cy.get(box).should('have.attr', 'data-state', 'warn');

    // Back to the template without the pixel, still unsaved.
    cy.get('.b-tabs nav a').eq(1).click();
    cy.get('select[name=template]').first().select(`${bareTpl}`);
    cy.wait('@recheck').its('response.body.data.view').should('eq', 'missing');
    cy.get('.b-tabs nav a').eq(0).click();
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-warn');
  });

  it('warns in the start confirmation and does not start when cancelled', () => {
    cy.get('[data-cy=btn-start]').click();
    cy.get(warnLines).should('contain', 'There is no view tracking.')
      .and('contain', '2 links do not record clicks.');
    cy.get(`${warnLines} strong`).should('have.length', 2);
    cy.get('[data-cy=send-ok]').should('contain', 'anyway');
    cy.get('[data-cy=send-cancel]').click();
    cy.get('.modal.is-active').should('not.exist');
    cy.request(`/api/campaigns/${ids.warn}`).its('body.data.status').should('eq', 'draft');
  });

  it('says "Schedule anyway" for a scheduled start', () => {
    cy.get('.b-tabs nav a').eq(0).click();
    cy.get('[data-cy=btn-send-later] .check').click();
    cy.get('.datepicker input').click();
    cy.get('.datepicker-header .control:nth-child(2) select').select((new Date().getFullYear() + 1).toString());
    cy.get('.datepicker-body a.is-selectable:first').click();
    cy.get('body').click(1, 1);
    cy.get('[data-cy=btn-schedule]').click();
    cy.get(warnLines).should('contain', 'There is no view tracking.');
    cy.get('[data-cy=send-ok]').should('contain', 'Schedule for').and('contain', 'anyway');
    cy.get('[data-cy=send-cancel]').click();
    cy.request(`/api/campaigns/${ids.warn}`).its('body.data.status').should('eq', 'draft');
  });

  it('adds the tracking state to the test send notice', () => {
    // The send itself is stubbed here; the notice is what this checks.
    cy.intercept('POST', `/api/campaigns/${ids.warn}/test`, { statusCode: 200, body: { data: true } }).as('testSend');
    cy.get('.box .taginput input').type('demo@example.invalid{enter}');
    cy.contains('.box button', 'Send').click();
    cy.wait('@testSend').its('request.body.template_id').should('eq', bareTpl);
    cy.get('.toast.is-warning').should('contain', 'Test message sent')
      .and('contain', 'no view tracking').and('contain', '2 links without click tracking');
  });

  it('starts anyway when confirmed: it is a warning, not a block', () => {
    cy.intercept('PUT', `/api/campaigns/${ids.plain}/status`, { statusCode: 200, body: { data: {} } }).as('status');
    open('plain');
    cy.get(box).should('have.attr', 'data-state', 'warn');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-na').and('contain', 'Cannot be recorded');
    cy.get('[data-cy=tracking-click]').should('have.class', 'is-warn').and('contain', '1 of 2 links not tracked');
    cy.get('[data-cy=btn-start]').click();
    cy.get(warnLines).should('contain', '1 links do not record clicks.')
      .and('not.contain', 'view tracking');
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@status').its('request.body.status').should('eq', 'running');
  });

  it('follows the body as it is typed: a plain text mail with every link tracked is ok', () => {
    open('plain');
    cy.intercept('POST', check).as('typed');
    cy.get('.b-tabs nav a').eq(1).click();
    cy.get('textarea[name=content]').clear().type('Offer https://example.invalid/offer@TrackLink');
    cy.wait('@typed');
    cy.get('.b-tabs nav a').eq(0).click();
    cy.get(box).should('have.attr', 'data-state', 'ok');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-na');
    cy.get('[data-cy=tracking-click]').should('contain', 'All 1 links tracked');
  });

  it('says automatic for a visual e-mail', () => {
    open('visual');
    cy.get(box).should('have.attr', 'data-state', 'ok');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-ok').and('contain', 'Automatic');
    cy.get('[data-cy=tracking-click]').should('have.class', 'is-ok').and('contain', 'automatic');
  });

  it('checks again at start: a template edited elsewhere changes the answer', () => {
    open('ok');
    cy.get(box).should('have.attr', 'data-state', 'ok');
    cy.request('PUT', `/api/templates/${pixelTpl}`, { name: `With pixel ${stamp}`, type: 'campaign', body: barePixelless });
    cy.intercept('POST', check).as('atStart');
    cy.get('[data-cy=btn-start]').click();
    cy.wait('@atStart').its('response.body.data.view').should('eq', 'missing');
    cy.get(warnLines).should('contain', 'There is no view tracking.');
    cy.get('[data-cy=send-cancel]').click();
    cy.get(box).should('have.attr', 'data-state', 'warn');
  });

  it('follows the unsaved subject when the body depends on it', () => {
    cy.request('PUT', `/api/campaigns/${ids.ok}`, {
      name: `Tracking ok ${stamp}`,
      subject: 'with-pixel',
      type: 'regular',
      messenger: 'email',
      lists: [listId],
      content_type: 'html',
      template_id: bareTpl,
      body: '{{ if eq .Campaign.Subject "with-pixel" }}{{ TrackView }}{{ end }}<p>Hello</p>',
    });
    open('ok');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-ok');
    cy.intercept('POST', check).as('subject');
    cy.get('input[name=subject]').clear().type('without-pixel');
    cy.wait('@subject').its('request.body.subject').should('eq', 'without-pixel');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-warn').and('contain', 'Missing');
    cy.get(box).should('have.attr', 'data-state', 'warn');
  });

  it('starts with the attributes that are on the page: the check and the save agree', () => {
    cy.request('PUT', `/api/campaigns/${ids.ok}`, {
      name: `Tracking ok ${stamp}`,
      subject: 'Attributes',
      type: 'regular',
      messenger: 'email',
      lists: [listId],
      content_type: 'html',
      template_id: bareTpl,
      attribs: { track: false },
      body: '{{ if .Campaign.Attribs.track }}{{ TrackView }}{{ end }}<p>Hello</p>',
    });
    open('ok');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-warn');
    cy.intercept('GET', preflight).as('attribs');
    cy.intercept('PUT', `/api/campaigns/${ids.ok}`).as('save');
    cy.intercept('PUT', `/api/campaigns/${ids.ok}/status`, { statusCode: 200, body: { data: {} } }).as('start');
    cy.get('.b-tabs nav a').eq(2).click();
    cy.get('.b-tabs .tab-item:visible textarea').clear().type('{"track": true}', { parseSpecialCharSequences: false });
    cy.get('[data-cy=btn-start]').click();
    // Saved first, then the saved campaign is checked, then the question.
    cy.wait('@save').its('request.body.attribs.track').should('eq', true);
    cy.get('.modal.is-active').should('be.visible');
    cy.wait('@attribs').its('response.body.data.tracking.view').should('eq', 'manual');
    ready();
    cy.get(warnLines).should('not.exist');
    cy.get('[data-cy=send-ok]').click();
    cy.wait('@start');
    cy.get('@save.all').should('have.length', 1);
    cy.request(`/api/campaigns/${ids.ok}`).its('body.data.attribs.track').should('eq', true);
  });

  it('asks about what is saved: the window covers the page while the checks are read', () => {
    cy.request('PUT', `/api/campaigns/${ids.ok}`, {
      name: `Tracking ok ${stamp}`,
      subject: 'Attributes',
      type: 'regular',
      messenger: 'email',
      lists: [listId],
      content_type: 'html',
      template_id: bareTpl,
      attribs: { track: false },
      body: '{{ if .Campaign.Attribs.track }}{{ TrackView }}{{ end }}<p>Hello</p>',
    });
    open('ok');
    cy.get('.b-tabs nav a').eq(2).click();
    cy.intercept('GET', preflight, (req) => { req.on('response', (res) => { res.setDelay(1500); }); }).as('slowCheck');
    cy.intercept('PUT', `/api/campaigns/${ids.ok}/status`, { statusCode: 200, body: { data: {} } }).as('start');
    cy.get('[data-cy=btn-start]').click();
    // The checks are on their way and the page cannot be edited under the window.
    cy.get('[data-cy=send-loading]').should('be.visible');
    cy.get('[data-cy=send-ok]').should('be.disabled');
    // The question is about the campaign as it was saved: no pixel.
    cy.get(warnLines, { timeout: 10000 }).should('contain', 'There is no view tracking.');
    cy.request(`/api/campaigns/${ids.ok}`).its('body.data.attribs.track').should('eq', false);
    cy.get('[data-cy=send-ok]').should('contain', 'anyway').click();
    cy.wait('@start');
    // Nothing was saved after the question.
    cy.request(`/api/campaigns/${ids.ok}`).its('body.data.attribs.track').should('eq', false);
  });

  it('checks the status that the send will have, not the draft status a test send has', () => {
    cy.request('PUT', `/api/campaigns/${ids.ok}`, {
      name: `Tracking ok ${stamp}`,
      subject: 'Status',
      type: 'regular',
      messenger: 'email',
      lists: [listId],
      content_type: 'html',
      template_id: bareTpl,
      attribs: {},
      body: '{{ if eq .Campaign.Status "draft" }}{{ TrackView }}{{ end }}<p>Hello</p>',
    });
    open('ok');
    // Started, the campaign is "running": no pixel. The box says so.
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-warn');
    cy.get('@check').its('request.body.status').should('eq', 'running');
    // The window reads the saved campaign as the sender will: the same warning.
    cy.get('[data-cy=btn-start]').click();
    ready();
    cy.get(warnLines).should('contain', 'There is no view tracking.');
    cy.get('[data-cy=send-cancel]').click();
    cy.get('.modal.is-active').should('not.exist');
    // A test send goes out as the draft it is: with the pixel, and its notice says nothing.
    cy.intercept('POST', `/api/campaigns/${ids.ok}/test`, { statusCode: 200, body: { data: true } }).as('testSend');
    cy.get('.box .taginput input').type('demo@example.invalid{enter}');
    cy.contains('.box button', 'Send').click();
    cy.wait('@testSend');
    cy.get('.toast').should('contain', 'Test message sent').and('not.contain', 'no view tracking');
  });

  it('drops a start whose window is still open or loading when the page is left', () => {
    let slow = true;
    cy.intercept('PUT', '/api/campaigns/*/status').as('anyStatus');
    cy.intercept('GET', preflight, (req) => {
      if (slow) req.on('response', (res) => { res.setDelay(2500); });
    }).as('anyPreflight');
    const openFromList = (key) => {
      cy.contains('tbody tr a', `Tracking ${key} ${stamp}`).click();
      cy.location('pathname').should('eq', `/admin/campaigns/${ids[key]}`);
      cy.get(box, { timeout: 10000 }).should('have.attr', 'data-state').and('not.eq', 'checking');
    };
    // The draft may have a send time from an earlier test: its button is then "Schedule".
    cy.visit('/admin/campaigns');
    openFromList('warn');

    // Leave for the list and open another draft while the window is still reading.
    cy.get('[data-cy=btn-start], [data-cy=btn-schedule]').first().click();
    cy.get('[data-cy=send-loading]').should('be.visible');
    cy.window().then((win) => win.history.back());
    cy.location('pathname').should('eq', '/admin/campaigns');
    openFromList('visual');
    cy.wait(3000);
    cy.get('.modal.is-active').should('not.exist');
    cy.get('@anyStatus.all').should('have.length', 0);
    cy.request(`/api/campaigns/${ids.warn}`).its('body.data.status').should('eq', 'draft');

    // A window that is open goes away with its page, too.
    cy.then(() => { slow = false; });
    cy.window().then((win) => win.history.back());
    cy.location('pathname').should('eq', '/admin/campaigns');
    openFromList('warn');
    cy.get('[data-cy=btn-start], [data-cy=btn-schedule]').first().click();
    ready();
    cy.window().then((win) => win.history.back());
    cy.location('pathname').should('eq', '/admin/campaigns');
    cy.get('.modal.is-active').should('not.exist');
    cy.get('@anyStatus.all').should('have.length', 0);
  });

  it('warns when a replaced custom header is still sent', () => {
    cy.request('PUT', `/api/campaigns/${ids.ok}`, {
      name: `Tracking ok ${stamp}`,
      subject: 'Headers',
      type: 'regular',
      messenger: 'email',
      lists: [listId],
      content_type: 'html',
      template_id: bareTpl,
      headers: [{ 'X-No-Track': '1' }],
      body: '{{ $no := false }}{{ range .Campaign.Headers }}{{ if index . "X-No-Track" }}{{ $no = true }}{{ end }}{{ end }}'
        + '{{ if not $no }}{{ TrackView }}{{ end }}<p>Hello</p>',
    });
    open('ok');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-warn');
    cy.intercept('POST', check).as('headers');
    cy.intercept('PUT', `/api/campaigns/${ids.ok}/status`, { statusCode: 200, body: { data: {} } }).as('start');
    cy.get('textarea[name=headers]').clear().type('[{"X-Other": "1"}]', { parseSpecialCharSequences: false });
    // The server keeps the old header beside the new one, and the box says so already.
    cy.wait('@headers');
    cy.get('[data-cy=tracking-view]').should('have.class', 'is-warn');
    cy.get('[data-cy=btn-start]').click();
    cy.get(warnLines).should('contain', 'There is no view tracking.');
    cy.request(`/api/campaigns/${ids.ok}`).its('body.data.headers').should('deep.equal', [{ 'X-No-Track': '1', 'X-Other': '1' }]);
    cy.get('[data-cy=send-cancel]').click();
  });

  it('warns on the campaigns list too, for start and for schedule', () => {
    // The schedule test above saved a send time with the draft: take it off again.
    cy.request('PUT', `/api/campaigns/${ids.warn}`, {
      name: `Tracking warn ${stamp}`,
      subject: 'Tracking warn',
      type: 'regular',
      messenger: 'email',
      lists: [listId],
      content_type: 'richtext',
      template_id: bareTpl,
      body: mixedBody,
      send_at: null,
    });
    cy.visit('/admin/campaigns');
    cy.intercept('GET', preflight).as('listCheck');
    cy.intercept('PUT', '/api/campaigns/*/status', { statusCode: 200, body: { data: {} } }).as('listStatus');
    cy.contains('tbody tr', `Tracking warn ${stamp}`).find('[data-cy=btn-start]').click();
    cy.wait('@listCheck').its('response.body.data.tracking.state').should('eq', 'warn');
    cy.get(warnLines).should('contain', 'There is no view tracking.')
      .and('contain', '2 links do not record clicks.');
    cy.get('[data-cy=send-ok]').should('contain', 'anyway');
    cy.get('[data-cy=send-cancel]').click();
    cy.get('@listStatus.all').should('have.length', 0);

    // A draft with a send time has the schedule button.
    cy.request(`/api/campaigns/${ids.warn}`).then(({ body }) => {
      const c = body.data;
      cy.request('PUT', `/api/campaigns/${ids.warn}`, {
        name: c.name,
        subject: c.subject,
        type: 'regular',
        messenger: 'email',
        lists: [listId],
        content_type: c.content_type,
        template_id: c.template_id,
        body: c.body,
        send_at: new Date(Date.now() + 86400000 * 30).toISOString(),
      });
    });
    cy.visit('/admin/campaigns');
    cy.contains('tbody tr', `Tracking warn ${stamp}`).find('[data-cy=btn-schedule]').click();
    cy.wait('@listCheck');
    cy.get(warnLines).should('contain', 'There is no view tracking.');
    cy.get('[data-cy=send-ok]').should('contain', 'Schedule for').and('contain', 'anyway').click();
    cy.wait('@listStatus').its('request.body.status').should('eq', 'scheduled');

    // A campaign without a warning keeps the usual confirmation.
    cy.contains('tbody tr', `Tracking visual ${stamp}`).find('[data-cy=btn-start]').click();
    cy.wait('@listCheck').its('response.body.data.tracking.state').should('eq', 'ok');
    ready();
    cy.get(warnLines).should('not.exist');
    cy.get('[data-cy=send-ok]').should('contain', 'Send to').and('not.contain', 'anyway');
    cy.get('[data-cy=send-cancel]').click();
  });

  it('shows why a body that cannot be rendered was not checked', () => {
    cy.request('PUT', `/api/campaigns/${ids.plain}`, {
      name: `Tracking plain ${stamp}`,
      subject: 'Tracking plain',
      type: 'regular',
      messenger: 'email',
      lists: [listId],
      content_type: 'plain',
      body: 'Hello {{ .Subscriber.NoSuchField }}',
    });
    open('plain');
    cy.get(box).should('have.attr', 'data-state', 'failed');
    cy.get('[data-cy=tracking-error]').should('contain', 'NoSuchField');
    cy.get('[data-cy=tracking-view]').should('not.exist');

    cy.get('[data-cy=btn-start]').click();
    cy.get(warnLines).should('contain', 'The tracking check failed.');
    cy.get('[data-cy=send-cancel]').click();
  });
});
