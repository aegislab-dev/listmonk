// LST-50: the send time is read and shown in a chosen time zone (default KST); the stored moment
// (send_at) stays an instant and the zone is kept in attribs.axm_send_tz.
const stamp = Date.now();
const KEY = 'axm_send_tz';
let listId;
const ids = {};

const make = (key, extra) => cy.request('POST', '/api/campaigns', {
  name: `Schedule zone ${key} ${stamp}`,
  subject: 'Schedule zone',
  type: 'regular',
  lists: [listId],
  content_type: 'plain',
  body: 'x',
  ...extra,
}).then(({ body }) => { ids[key] = body.data.id; });

const open = (key) => cy.visit(`/admin/campaigns/${ids[key]}`);
const pickerValue = () => cy.get('[data-cy=send_at] .datepicker input').first();
const zoneButton = () => cy.get('[data-cy=send-tz-button]');
// The attributes tab's JSON: the zone key must not be there in either spelling (the frontend
// camelCases attribs keys from the API: axm_send_tz -> axmSendTz).
const attribsTab = (expected) => {
  cy.contains('.b-tabs nav a', /attrib/i).click();
  cy.get('textarea[data-cy=campaign-attribs]').should('be.visible').invoke('val').should((v) => {
    expect(v).not.to.match(/axm_?send_?tz/i);
    expect(JSON.parse(v)).to.deep.eq(expected);
  });
};

describe('Scheduled send time zone', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', {
      name: `Schedule zone list ${stamp}`, type: 'private', optin: 'single', no_team: true,
    }).then(({ body }) => {
      listId = body.data.id;
      // 2030-01-15 is a Tuesday. 14:00 UTC is 09:00 in New York (EST) and 23:00 in Korea.
      make('ny', { send_at: '2030-01-15T14:00:00.000Z', attribs: { [KEY]: 'America/New_York' } });
      // An alias of New York: the browser lists it as America/New_York.
      make('alias', { send_at: '2030-01-15T14:00:00.000Z', attribs: { [KEY]: 'US/Eastern' } });
      make('ime', { send_at: '2030-01-15T00:00:00.000Z' });
      make('nyattr', { send_at: '2030-01-15T14:00:00.000Z', attribs: { [KEY]: 'America/New_York', note: 'hi' } });
      // No zone stored: KST. 00:00 UTC is 09:00 in Korea.
      make('kst', { send_at: '2030-01-15T00:00:00.000Z' });
      make('empty', {});
      make('sched', {});
      make('attr', {});
      // 2030-03-10 02:30 KST: a time that does not exist on a New York computer that day.
      make('dst', { send_at: '2030-03-09T17:30:00.000Z' });
    });
  });

  beforeEach(() => {
    cy.loginAndVisit('/admin/campaigns');
  });

  it('shows a stored zone: the time in that zone, its short name and KST first underneath', () => {
    open('ny');
    pickerValue().should('have.value', '2030-01-15 09:00');
    zoneButton().should('contain', 'EST');
    cy.get('[data-cy=send-at-zones] .zone').first().should('have.attr', 'data-tz', 'Asia/Seoul')
      .and('contain', '01/15').and('contain', '23:00');
    cy.get('[data-cy=send-at-zones] .zone[data-tz="America/New_York"]').should('not.exist');
    cy.get('[data-cy=send-at-zones] .zone[data-tz="America/Los_Angeles"]').should('contain', '06:00');
  });

  it('defaults to KST when no zone is stored', () => {
    open('kst');
    pickerValue().should('have.value', '2030-01-15 09:00');
    zoneButton().should('contain', 'KST');
    cy.get('[data-cy=send-at-zones] .zone[data-tz="Asia/Seoul"]').should('not.exist');
    cy.get('[data-cy=send-at-zones] .zone[data-tz="America/New_York"]').should('contain', '01/14').and('contain', '19:00');
  });

  it('searches every time zone and keeps the moment when the zone changes', () => {
    open('kst');
    zoneButton().click();
    cy.get('[data-cy=send-tz-panel]').should('be.visible');
    cy.get('[data-cy=send-tz-panel] [data-tz]').should('have.length.greaterThan', 100);
    cy.get('[data-cy=send-tz-panel] [data-tz="Asia/Seoul"]').should('have.class', 'is-selected');
    cy.get('[data-cy=send-tz-search]').type('london');
    cy.get('[data-cy=send-tz-panel] [data-tz]').first().should('have.attr', 'data-tz', 'Europe/London');
    cy.get('[data-cy=send-tz-panel] [data-tz="Europe/London"]').click();
    cy.get('[data-cy=send-tz-panel]').should('not.exist');
    zoneButton().should('contain', 'GMT');
    // The same moment (00:00 UTC) in London in January.
    pickerValue().should('have.value', '2030-01-15 00:00');
    cy.get('[data-cy=send-at-zones] .zone').first().should('have.attr', 'data-tz', 'Asia/Seoul').and('contain', '09:00');

    // The chosen zone is offered under "recent" next time, even though London is also a frequent zone.
    zoneButton().click();
    cy.get('[data-cy=send-tz-recent] [data-tz="Europe/London"]').should('exist');
    cy.get('[data-cy=send-tz-search]').type('{esc}');
    cy.get('[data-cy=send-tz-panel]').should('not.exist');
  });

  it('lists the last chosen zones newest first under recent', () => {
    open('kst');
    ['paris', 'new york'].forEach((q) => {
      zoneButton().click();
      cy.get('[data-cy=send-tz-search]').type(q);
      cy.get('[data-cy=send-tz-panel] [data-tz]').first().click();
    });
    zoneButton().click();
    cy.get('[data-cy=send-tz-recent] [data-tz]').then(($rows) => {
      const tzs = [...$rows].map((r) => r.getAttribute('data-tz'));
      expect(tzs.slice(0, 2)).to.deep.eq(['America/New_York', 'Europe/Paris']);
    });
  });

  it('shows every match of a wide search, not only the first few', () => {
    open('kst');
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('America');
    cy.get('[data-cy=send-tz-panel] [data-tz]').should('have.length.greaterThan', 100);
    cy.get('[data-cy=send-tz-panel] [data-tz]').last().scrollIntoView().should('be.visible').click();
    cy.get('[data-cy=send-tz-panel]').should('not.exist');
  });

  it('accepts a zone typed in full when the browser cannot list zones', () => {
    cy.visit(`/admin/campaigns/${ids.kst}`, {
      onBeforeLoad(win) { win.Intl.supportedValuesOf = undefined; },
    });
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('europe/paris');
    cy.get('[data-cy=send-tz-panel] [data-tz="Europe/Paris"]').first().click();
    zoneButton().should('contain', 'UTC+1');
    pickerValue().should('have.value', '2030-01-15 01:00');

    // Names without a slash work too.
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('EST5EDT');
    // A browser may give such an old name its current one (America/New_York); one row either way.
    cy.get('[data-cy=send-tz-panel] [data-tz]').first().click();
    pickerValue().should('have.value', '2030-01-14 19:00');
  });

  it('puts the time zone button under the input on a narrow screen, inside its own row', () => {
    cy.viewport(320, 700);
    open('ny');
    zoneButton().should('be.visible').then(($b) => {
      const r = $b[0].getBoundingClientRect();
      const row = Cypress.$('[data-cy=send_at]')[0].getBoundingClientRect();
      const input = Cypress.$('[data-cy=send_at] .datepicker input')[0].getBoundingClientRect();
      expect(r.left).to.be.gte(row.left - 1);
      expect(r.right).to.be.lte(row.right + 1);
      expect(r.top).to.be.gte(input.bottom - 1);
      // The arrow at the end of the button is on the screen, with no sideways scrolling. Measure against
      // the page's own width: cy.viewport() does not change Cypress.config('viewportWidth').
      const pageWidth = $b[0].ownerDocument.documentElement.clientWidth;
      expect(pageWidth, 'page width').to.be.lte(320);
      const arrow = Cypress.$('[data-cy=send-tz-button] .tzp-caret')[0].getBoundingClientRect();
      expect(arrow.width, 'arrow width').to.be.gt(0);
      expect(arrow.left).to.be.gte(0);
      expect(arrow.right).to.be.lte(pageWidth);
    });
  });

  it('hides the stored zone from the attributes tab and keeps the other attributes', () => {
    cy.intercept('PUT', '/api/campaigns/*').as('save');
    open('nyattr');
    zoneButton().should('contain', 'EST');
    attribsTab({ note: 'hi' });
    cy.get('button[data-cy=btn-save]').first().click();
    cy.wait('@save').its('request.body.attribs').should('deep.eq', { note: 'hi', [KEY]: 'America/New_York' });
    cy.reload();
    attribsTab({ note: 'hi' });
  });

  it('Enter in the zone search picks the first match and does not submit a new campaign', () => {
    cy.intercept('POST', '/api/campaigns').as('create');
    cy.visit(`/admin/campaigns/new?list_id=${listId}`);
    // The chosen lists only (the dropdown has tags too, for dynamic lists).
    cy.get('.list-selector .list-tags .tag.list').should('have.length', 1).and('have.attr', 'data-id', String(listId));
    cy.get('input[name=name]').type(`Schedule zone enter ${stamp}`);
    cy.get('input[name=subject]').type('Enter');
    cy.get('[data-cy=btn-send-later] .check').click();
    // No {enter} here: in the new campaign form that would submit it.
    pickerValue().clear().type('2030-02-01 09:00');
    cy.get('body').click(1, 1);
    pickerValue().should('have.value', '2030-02-01 09:00');
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('new york');
    cy.get('[data-cy=send-tz-panel] [data-tz="America/New_York"]').should('exist');
    cy.get('[data-cy=send-tz-search]').type('{enter}');
    cy.get('[data-cy=send-tz-panel]').should('not.exist');
    zoneButton().should('contain', 'EST');
    // Changing the zone keeps the moment: 09:00 KST is 19:00 the day before in New York.
    pickerValue().should('have.value', '2030-01-31 19:00');
    // Still on the new campaign form: nothing was created.
    cy.wait(500);
    cy.get('@create.all').should('have.length', 0);
    cy.location('pathname').should('eq', '/admin/campaigns/new');
    cy.get('[data-cy=btn-continue]').should('be.visible');

    // The form was complete: Continue creates it, with the zone picked by Enter.
    cy.get('[data-cy=btn-continue]').click();
    cy.wait('@create').its('request.body').should((b) => {
      expect(b.attribs[KEY]).to.eq('America/New_York');
      expect(new Date(b.send_at).toISOString()).to.eq('2030-02-01T00:00:00.000Z');
    });
  });

  it('the Enter that ends a Korean (IME) composition picks nothing; the search follows the composed text', () => {
    const cdp = (command, params) => Cypress.automation('remote:debugger:protocol', { command, params });
    // A trusted Enter key press as the browser gets it (keyDown with text makes the keypress that
    // submits a form).
    const enterDown = () => cdp('Input.dispatchKeyEvent', {
      type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13, text: '\r',
    });
    const enterUp = () => cdp('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
    cy.intercept('PUT', '/api/campaigns/*').as('save');
    open('ime');
    zoneButton().should('contain', 'KST');
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').focus();

    // "파" typed, then "리" still being composed: the box shows 파리 and the results are for 파리
    // already, not for 파 (whose first match is Paraguay).
    cy.then(() => cdp('Input.insertText', { text: '파' }));
    cy.then(() => cdp('Input.imeSetComposition', { text: '리', selectionStart: 1, selectionEnd: 1 }));
    cy.get('[data-cy=send-tz-search]').should('have.value', '파리');
    cy.get('[data-cy=send-tz-panel] [data-tz]').first().should('have.attr', 'data-tz', 'Europe/Paris');
    cy.then(() => cdp('Input.insertText', { text: '리' }));
    cy.get('[data-cy=send-tz-search]').clear();

    // Linux IBus: one Enter press while "파" is composed fires a composing keydown, compositionend
    // (the syllable is committed) and then a plain Enter keydown, before the key is released.
    cy.then(() => cdp('Input.imeSetComposition', { text: '파', selectionStart: 1, selectionEnd: 1 }));
    cy.get('[data-cy=send-tz-search]').should('have.value', '파');
    cy.get('[data-cy=send-tz-panel] [data-tz]').first().should('have.attr', 'data-tz', 'America/Asuncion');
    cy.then(() => cdp('Input.dispatchKeyEvent', {
      type: 'rawKeyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13,
    }));
    cy.then(() => cdp('Input.insertText', { text: '파' }));
    cy.then(enterDown);
    cy.then(enterUp);
    // Nothing is picked and the form is not submitted.
    cy.get('[data-cy=send-tz-search]').should('have.value', '파');
    cy.get('[data-cy=send-tz-panel]').should('exist');
    zoneButton().should('contain', 'KST');
    cy.wait(300);
    cy.get('@save.all').should('have.length', 0);

    // Windows / macOS: the composing keydown only, then the key is released.
    cy.get('[data-cy=send-tz-search]').clear();
    cy.then(() => cdp('Input.insertText', { text: '파' }));
    cy.then(() => cdp('Input.imeSetComposition', { text: '리', selectionStart: 1, selectionEnd: 1 }));
    cy.then(() => cdp('Input.dispatchKeyEvent', {
      type: 'rawKeyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13,
    }));
    cy.then(() => cdp('Input.insertText', { text: '리' }));
    cy.then(enterUp);
    cy.get('[data-cy=send-tz-panel]').should('exist');
    zoneButton().should('contain', 'KST');

    // The next Enter press picks the first match: Paris. The moment does not move.
    cy.get('[data-cy=send-tz-search]').should('have.value', '파리');
    cy.then(enterDown);
    cy.then(enterUp);
    cy.get('[data-cy=send-tz-panel]').should('not.exist');
    zoneButton().should('contain', 'UTC+1');
    pickerValue().should('have.value', '2030-01-15 01:00');
    cy.get('@save.all').should('have.length', 0);
  });

  it('a stored alias (US/Eastern) is the listed zone: marked in the picker, left out of the line', () => {
    open('alias');
    pickerValue().should('have.value', '2030-01-15 09:00');
    zoneButton().should('contain', 'EST');
    cy.get('[data-cy=send-at-zones] .zone[data-tz="America/New_York"]').should('not.exist');
    cy.get('[data-cy=send-at-zones] .zone').first().should('have.attr', 'data-tz', 'Asia/Seoul');
    zoneButton().click();
    cy.get('[data-cy=send-tz-panel] .tzp-row.is-selected').should('have.length.gte', 1).each(($r) => {
      expect($r.attr('data-tz')).to.eq('America/New_York');
    });
    cy.get('[data-cy=send-tz-panel] .tzp-row[data-tz="America/New_York"]').each(($r) => {
      expect($r.hasClass('is-selected'), 'every New York row is marked').to.eq(true);
    });
  });

  it('keeps the date and time readable next to the zone on tablet and small desktop widths', () => {
    [[820, 1000], [1024, 768], [1280, 800], [1440, 900], [390, 800]].forEach(([w, h]) => {
      cy.viewport(w, h);
      open('kst');
      pickerValue().should('have.value', '2030-01-15 09:00').should(($i) => {
        const input = $i[0];
        // The whole value fits in the box (no clipped text) and the box is not squeezed.
        expect(input.scrollWidth, `text fits at ${w}px`).to.be.lte(input.clientWidth + 1);
        expect(input.getBoundingClientRect().width, `input width at ${w}px`).to.be.gte(150);
      });
      zoneButton().should('be.visible').then(($b) => {
        const r = $b[0].getBoundingClientRect();
        const input = Cypress.$('[data-cy=send_at] .datepicker input')[0].getBoundingClientRect();
        const pageWidth = $b[0].ownerDocument.documentElement.clientWidth;
        // Beside the input or under it, never over it, and on the screen.
        expect(r.left >= input.right - 2 || r.top >= input.bottom - 1, `button placement at ${w}px`).to.eq(true);
        expect(r.right, `button on screen at ${w}px`).to.be.lte(pageWidth);
        // Where there is room (a wide screen), beside the input as in the approved design.
        if (w >= 1440) expect(r.left, 'beside the input').to.be.gte(input.right - 2);
      });
    });
  });

  it('searches by Korean name', () => {
    open('kst');
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('뉴욕');
    cy.get('[data-cy=send-tz-panel] [data-tz]').first().should('have.attr', 'data-tz', 'America/New_York');
  });

  it('searches by country name, in English or Korean', () => {
    open('kst');
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('France');
    cy.get('[data-cy=send-tz-panel] [data-tz="Europe/Paris"]').should('exist');
    cy.get('[data-cy=send-tz-search]').clear().type('프랑스');
    cy.get('[data-cy=send-tz-panel] [data-tz="Europe/Paris"]').should('exist');
  });

  it('saves the typed time as the moment in the chosen zone, and the zone in attribs', () => {
    cy.intercept('PUT', '/api/campaigns/*').as('save');
    open('empty');
    cy.get('[data-cy=btn-send-later] .check').click();
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('new york');
    cy.get('[data-cy=send-tz-panel] [data-tz="America/New_York"]').click();
    cy.get('[data-cy=send_at] .datepicker input').first().clear().type('2030-02-01 09:00{enter}');
    cy.get('body').click(1, 1);
    cy.get('[data-cy=send-at-zones] .zone').first().should('contain', '02/01').and('contain', '23:00');
    cy.get('button[data-cy=btn-save]').first().click();
    cy.wait('@save').its('request.body').should((b) => {
      // 09:00 in New York in February is 14:00 UTC, whatever the zone of this browser is.
      expect(new Date(b.send_at).toISOString()).to.eq('2030-02-01T14:00:00.000Z');
      expect(b.attribs[KEY]).to.eq('America/New_York');
    });
    cy.request(`/api/campaigns/${ids.empty}`).its('body.data').should((d) => {
      expect(new Date(d.send_at).toISOString()).to.eq('2030-02-01T14:00:00.000Z');
      expect(d.attribs[KEY]).to.eq('America/New_York');
    });

    // Reopen: the same wall time and zone. The attributes tab does not show the key.
    cy.reload();
    pickerValue().should('have.value', '2030-02-01 09:00');
    zoneButton().should('contain', 'EST');
    attribsTab({});
  });

  it('saving an opened campaign without touching the time keeps the moment', () => {
    cy.intercept('PUT', '/api/campaigns/*').as('save');
    open('ny');
    pickerValue().should('have.value', '2030-01-15 09:00');
    cy.get('button[data-cy=btn-save]').first().click();
    cy.wait('@save').its('request.body').should((b) => {
      expect(new Date(b.send_at).toISOString()).to.eq('2030-01-15T14:00:00.000Z');
      expect(b.attribs[KEY]).to.eq('America/New_York');
    });
  });

  it('starts the schedule straight from the screen without losing the zone', () => {
    cy.intercept('PUT', '/api/campaigns/*').as('save');
    cy.intercept('PUT', '/api/campaigns/*/status').as('status');
    open('sched');
    cy.get('[data-cy=btn-send-later] .check').click();
    zoneButton().click();
    cy.get('[data-cy=send-tz-search]').type('new york');
    cy.get('[data-cy=send-tz-panel] [data-tz="America/New_York"]').click();
    cy.get('[data-cy=send_at] .datepicker input').first().clear().type('2030-02-01 09:00{enter}');
    cy.get('body').click(1, 1);
    cy.get('button[data-cy=btn-schedule]').click();
    cy.get('.modal button.is-primary').first().click();
    cy.wait('@save').its('request.body').should((b) => {
      expect(new Date(b.send_at).toISOString()).to.eq('2030-02-01T14:00:00.000Z');
      expect(b.attribs[KEY]).to.eq('America/New_York');
    });
    cy.wait('@status');
    cy.request(`/api/campaigns/${ids.sched}`).its('body.data').should((d) => {
      expect(d.status).to.eq('scheduled');
      expect(d.attribs[KEY]).to.eq('America/New_York');
    });
  });

  it('does not save an attributes tab that is not an object', () => {
    cy.intercept('PUT', '/api/campaigns/*').as('save');
    open('attr');
    cy.contains('.b-tabs nav a', /attrib/i).click();
    cy.get('.tab-content textarea').filter(':visible').first().clear().type('["tag"]', { parseSpecialCharSequences: false });
    cy.get('button[data-cy=btn-save]').first().click();
    cy.get('@save.all').should('have.length', 0);
    cy.request(`/api/campaigns/${ids.attr}`).its('body.data.attribs').should('deep.eq', {});
  });

  it('shows the campaign list time in the zone of each campaign, with its short name', () => {
    cy.visit('/admin/campaigns');
    cy.contains('tr', `Schedule zone ny ${stamp}`).find('.scheduled').should('contain', '09:00 EST');
    cy.contains('tr', `Schedule zone kst ${stamp}`).find('.scheduled').should('contain', '09:00 KST');
    // 02:30 KST is shown as it is, also on a computer whose own clock skips that time.
    cy.contains('tr', `Schedule zone dst ${stamp}`).find('.scheduled').should('contain', '02:30 KST');
  });

  it('turning the schedule off removes the zone', () => {
    cy.intercept('PUT', '/api/campaigns/*').as('save');
    open('ny');
    cy.get('[data-cy=btn-send-later] .check').click();
    cy.get('button[data-cy=btn-save]').first().click();
    cy.wait('@save').its('request.body').should((b) => {
      expect(b.send_at).to.eq(null);
      expect(b.attribs).to.not.have.property(KEY);
    });
    cy.request(`/api/campaigns/${ids.ny}`).its('body.data.attribs').should('not.have.property', KEY);
  });

  it('rejects a time zone that does not exist', () => {
    ['Mars/Phobos', '', 9, 'Asia/Seoul\n'].forEach((bad) => {
      cy.request({
        method: 'POST',
        url: '/api/campaigns',
        failOnStatusCode: false,
        body: {
          name: `Bad zone ${stamp}`, subject: 's', type: 'regular', lists: [listId], content_type: 'plain', body: 'x', attribs: { [KEY]: bad },
        },
      }).its('status').should('eq', 400);
    });
  });
});
