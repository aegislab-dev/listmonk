// All data is synthetic. These tests use an already running disposable server; never call
// resetDB. The two check APIs are answered here, so nothing depends on real DNS; the
// unsubscribe and the import go to the real server.
const stamp = Date.now();
const listName = `Address check ${stamp}`;
const importListName = `Address import ${stamp}`;
const listCheck = '/api/lists/address-check';
const importCheck = '/api/subscribers/address-check';
const emails = [0, 1, 2, 3].map((n) => `addr-${stamp}-${n}@example.invalid`);

let listId;
let campaignId;
const ids = [];

// Recipient 2 is in two classes. The tags come from the server, as the lists can be cut short.
const tags = { 1: ['dead'], 2: ['dead', 'role'], 3: ['role'] };
const cls = (rows, direct = () => true) => ({
  count: rows.length,
  samples: rows.map((n) => emails[n]),
  ids: rows.map((n) => ids[n]),
  emails: rows.map((n) => emails[n]),
  tags: rows.map((n) => tags[n]),
  direct: rows.map(direct),
  truncated: false,
});
const empty = { count: 0, truncated: false };
// Four recipients: 0 is fine, 1 has a dead domain, 2 is dead and a role address, 3 is a role address.
const risky = () => ({
  total: 4,
  risky: 3,
  confirmed: 2,
  warning: 1,
  unknown: 0,
  pending: 0,
  too_large: false,
  limit: 500000,
  detail: true,
  linked: ['Tied source list'],
  linked_hidden: 2,
  targets: [listId],
  checked_lists: 1,
  classes: {
    format: empty, dead: cls([1, 2]), nullmx: empty, disposable: empty, role: cls([2, 3]), unknown: empty,
  },
});
const clean = (total) => ({
  total,
  risky: 0,
  confirmed: 0,
  warning: 0,
  unknown: 0,
  pending: 0,
  too_large: false,
  limit: 500000,
  detail: true,
  linked: [],
  linked_hidden: 0,
  targets: [listId],
  checked_lists: 1,
  classes: {
    format: empty, dead: empty, nullmx: empty, disposable: empty, role: empty, unknown: empty,
  },
});

describe('Address check before sending and on import', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', {
      name: listName, type: 'private', optin: 'single', no_team: true,
    }).then(({ body }) => {
      listId = body.data.id;
      emails.forEach((email, n) => {
        cy.request('POST', '/api/subscribers', {
          name: `Synthetic ${n}`, email, status: 'enabled', lists: [listId],
        }).then(({ body: sub }) => { ids[n] = sub.data.id; });
      });
      cy.request('POST', '/api/campaigns', {
        name: `Address check campaign ${stamp}`, subject: 'Address check', type: 'regular', lists: [listId],
      }).then(({ body: campaign }) => { campaignId = campaign.data.id; });
    });
    cy.request('POST', '/api/lists', {
      name: importListName, type: 'private', optin: 'single', no_team: true,
    });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
    cy.loginAndVisit('/admin/campaigns');
  });

  it('shows one line under the lists, the classes, the list of addresses, and unsubscribes through the ordinary API', () => {
    let answer = risky;
    cy.intercept('POST', listCheck, (req) => req.reply({ data: answer() })).as('check');
    cy.intercept('PUT', '/api/subscribers/lists').as('unsubscribe');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@check').its('request.body').should('deep.equal', { list_ids: [listId], optin: false });

    // The panel sits between the list selector and the recent-send exclusion.
    cy.get('[data-cy=address-check]').should('have.class', 'is-risk').then(($panel) => {
      expect($panel.prevAll('.list-selector')).to.have.length(1);
      expect($panel.nextAll('[data-cy=recent-exclude]')).to.have.length(1);
    });
    cy.get('[data-cy=address-summary]').should('contain', '4').and('contain', '3').and('contain', '2').and('contain', '1');
    cy.get('[data-cy=address-body]').should('not.exist');

    cy.get('[data-cy=address-toggle]').click();
    cy.get('[data-cy=address-group-confirmed] [data-cy=address-row-dead]').should('contain', emails[1]).and('contain', '2');
    cy.get('[data-cy=address-group-warning] [data-cy=address-row-role]').should('contain', emails[2]);
    cy.get('[data-cy=address-group-unknown]').should('not.exist');
    cy.get('[data-cy=address-row-format]').should('not.exist');
    cy.get('[data-cy=address-note]').should('be.visible');

    // The list shows every class of an address.
    cy.get('[data-cy=address-list-role]').click();
    cy.get('[data-cy=address-modal] tbody tr').should('have.length', 2);
    cy.contains('[data-cy=address-modal] tbody tr', emails[2]).find('.ac-chip').should('have.length', 2);
    cy.contains('[data-cy=address-modal] tbody tr', emails[3]).find('.ac-chip').should('have.length', 1);
    cy.get('[data-cy=address-modal] footer button').first().click();
    cy.get('[data-cy=address-modal]').should('not.exist');

    // Cancelling changes nothing; confirming sends the listed IDs for the chosen list.
    // The confirmation names the chosen list and the lists a draw ties to it.
    cy.get('[data-cy=address-note]').should('contain', 'Tied source list');
    cy.get('[data-cy=address-unsub-dead]').click();
    cy.get('.modal.dialog [data-cy=warn-box]').should('contain', listName).and('contain', 'Tied source list').and('contain', '2');
    cy.get('.modal.dialog footer button').first().click();
    cy.get('.modal.dialog').should('not.exist');

    cy.get('[data-cy=address-unsub-dead]').click();
    cy.then(() => { answer = () => ({ ...clean(2), risky: 1, warning: 1, classes: { ...clean(2).classes, role: cls([3]) } }); });
    cy.get('.modal.dialog footer button.is-danger').click();
    cy.wait('@unsubscribe').then(({ request, response }) => {
      expect(request.body).to.deep.equal({ ids: [ids[1], ids[2]], action: 'unsubscribe', target_list_ids: [listId] });
      expect(response.statusCode).to.eq(200);
    });
    // It checks again after the change.
    cy.wait('@check');
    cy.get('[data-cy=address-row-dead]').should('not.exist');
    cy.get('[data-cy=address-row-role]').should('contain', emails[3]);

    // The real server unsubscribed exactly those two and wrote the audit row.
    [0, 1, 2, 3].forEach((n) => {
      cy.request(`/api/subscribers/${ids[n]}`).then(({ body }) => {
        const sub = body.data.lists.find((l) => l.id === listId);
        expect(sub.subscription_status).to.eq(n === 1 || n === 2 ? 'unsubscribed' : 'unconfirmed');
      });
    });
    cy.request('/api/audit?action=subscriber.lists_unsubscribe&per_page=5').then(({ body }) => {
      expect(body.data.results[0].action).to.eq('subscriber.lists_unsubscribe');
      expect(body.data.results[0].count).to.eq(2);
    });
  });

  it('is one green line without risk, hides addresses without permission, and asks again while lookups are pending', () => {
    let calls = 0;
    cy.intercept('POST', listCheck, (req) => {
      calls += 1;
      req.reply({ data: calls === 1 ? { ...clean(4), pending: 2 } : clean(4) });
    }).as('check');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@check');
    cy.get('[data-cy=address-check]').should('have.class', 'is-ok');
    cy.get('[data-cy=address-summary]').should('contain', '2');
    // Pending lookups are asked about again after three seconds.
    cy.wait('@check', { timeout: 10000 });
    cy.get('[data-cy=address-toggle]').should('be.disabled');
    cy.get('[data-cy=address-body]').should('not.exist');
    cy.wait(3500).then(() => expect(calls).to.eq(2));

    // Counts only: no example, no list, no unsubscribe.
    cy.intercept('POST', listCheck, {
      data: {
        ...risky(), detail: false, classes: { ...clean(4).classes, dead: { count: 2, truncated: false }, role: { count: 2, truncated: false } },
      },
    }).as('counts');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@counts');
    cy.get('[data-cy=address-toggle]').click();
    cy.get('[data-cy=address-row-dead]').should('contain', '2').and('not.contain', '@');
    cy.get('[data-cy=address-list-dead], [data-cy=address-unsub-dead]').should('not.exist');

    // Someone who is in only through a dynamic list cannot be unsubscribed here: no button
    // for a class with nobody else, and only the others are sent for a mixed class.
    cy.intercept('POST', listCheck, {
      data: {
        ...risky(),
        // A tied list the user cannot see is still announced, as a count.
        linked: [],
        linked_hidden: 1,
        classes: { ...clean(4).classes, dead: cls([1, 2], () => false), role: cls([2, 3], (n) => n === 3) },
      },
    }).as('indirect');
    cy.intercept('PUT', '/api/subscribers/lists', { data: true }).as('unsubscribe');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@indirect');
    cy.get('[data-cy=address-toggle]').click();
    cy.get('[data-cy=address-list-dead]').should('exist');
    cy.get('[data-cy=address-unsub-dead]').should('not.exist');
    cy.get('[data-cy=address-list-dead]').click();
    cy.get('[data-cy=address-modal] .ac-indirect').should('have.length', 2);
    cy.get('[data-cy=address-modal-unsub]').should('not.exist');
    cy.get('[data-cy=address-modal] footer button').first().click();
    cy.get('[data-cy=address-note]').should('contain', '1 other lists');
    cy.get('[data-cy=address-unsub-role]').click();
    cy.get('.modal.dialog [data-cy=warn-box]').should('contain', '1 other lists');
    cy.get('.modal.dialog footer button.is-danger').click();
    cy.wait('@unsubscribe').its('request.body.ids').should('deep.equal', [ids[3]]);

    // Without a list the user may manage there is nothing to unsubscribe from.
    cy.intercept('POST', listCheck, { data: { ...risky(), targets: [] } }).as('readOnly');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@readOnly');
    cy.get('[data-cy=address-toggle]').click();
    cy.get('[data-cy=address-list-dead]').should('exist');
    cy.get('[data-cy=address-unsub-dead], [data-cy=address-unsub-role]').should('not.exist');

    // Lists the user cannot see were not checked: that is said, not shown as "no risk".
    cy.intercept('POST', listCheck, { data: { ...clean(0), targets: [], checked_lists: 0 } }).as('noAccess');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@noAccess');
    cy.get('[data-cy=address-check]').should('have.class', 'is-busy').and('not.have.class', 'is-ok');
    cy.get('[data-cy=address-summary]').should('contain', 'Not checked');

    // Some of the lists were not checked (the user cannot see them): the counts are those of
    // the checked lists only, never a green "no risky addresses" for the whole send.
    cy.intercept('POST', listCheck, { data: { ...clean(1), unchecked_lists: 1 } }).as('partial');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@partial');
    cy.get('[data-cy=address-check]').should('have.class', 'is-busy').and('not.have.class', 'is-ok');
    cy.get('[data-cy=address-summary]').should('contain', '1 checked recipients');
    cy.get('[data-cy=address-partial]').should('contain', '1 list(s) you cannot see were not checked');
    cy.intercept('POST', listCheck, { data: { ...risky(), unchecked_lists: 2 } }).as('partialRisky');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@partialRisky');
    cy.get('[data-cy=address-check]').should('have.class', 'is-risk');
    cy.get('[data-cy=address-summary]').should('contain', 'Of 4 checked recipients');
    cy.get('[data-cy=address-partial]').should('contain', '2 list(s)');
    cy.intercept('POST', listCheck, { data: clean(4) }).as('whole');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@whole');
    cy.get('[data-cy=address-check]').should('have.class', 'is-ok');
    cy.get('[data-cy=address-partial]').should('not.exist');

    // A failed check is one grey line and nothing else.
    cy.intercept('POST', listCheck, { statusCode: 500, body: { message: 'Synthetic failure' } }).as('failed');
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.wait('@failed');
    cy.get('[data-cy=address-check]').should('have.class', 'is-busy');
    cy.get('[data-cy=address-summary]').should('contain', 'Synthetic failure');
    cy.get('[data-cy=recent-exclude]').should('be.visible');
  });

  it('counts risky rows in the import review, leaves them out by the switches, and checks the list when the import ends', () => {
    const rows = [0, 1, 2, 3, 4, 5].map((n) => `import-${stamp}-${n}@example.invalid`);
    cy.intercept('POST', importCheck, (req) => {
      expect(req.body.emails).to.deep.equal(rows);
      req.reply({
        data: {
          total: 6,
          risky: 4,
          confirmed: 2,
          warning: 2,
          unknown: 1,
          pending: 0,
          classes: {},
          issues: {
            1: ['dead'], 2: ['dead', 'role'], 3: ['role'], 4: ['disposable'], 5: ['unknown'],
          },
        },
      });
    }).as('importCheck');
    cy.intercept('POST', listCheck, { data: clean(4) }).as('listCheck');

    cy.visit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('form.box').should('be.visible');
    cy.get('.list-selector input').type(importListName);
    cy.contains('.list-selector .autocomplete a', importListName).click();
    cy.get('input[type=file]').selectFile({
      contents: Cypress.Buffer.from(`email,name\n${rows.map((email, n) => `${email},Synthetic ${n}`).join('\n')}\n`),
      fileName: 'addr.csv',
      mimeType: 'text/csv',
    }, { force: true });
    // A file with the standard header starts in as-is mode, which has no review and no check.
    cy.get('[data-cy=map-mode-raw] input').should('be.checked').and('not.be.disabled');
    cy.get('[data-cy=map-busy]').should('not.exist');
    cy.get('[data-cy=map-mode-mapping]').click();
    cy.wait('@importCheck');

    // Confirmed-risk rows are left out by default; role and disposable rows are only counted.
    cy.get('[data-cy=map-address-summary]').should('contain', '6').and('contain', '4');
    cy.get('[data-cy=map-address-dead]').should('contain', `3 (${rows[1]})`).and('contain', `4 (${rows[2]})`);
    cy.get('[data-cy=map-address-role]').should('contain', `5 (${rows[3]})`);
    cy.get('[data-cy=map-address-disposable]').should('contain', `6 (${rows[4]})`);
    cy.get('[data-cy=map-address-unknown]').should('contain', `7 (${rows[5]})`);
    // No switch for the confirmed rows: they are never imported (a fixed line says so).
    cy.get('[data-cy=map-address-exclude-confirmed]').should('not.exist');
    cy.get('[data-cy=map-address-fixed]').should('contain', '2');
    cy.get('[data-cy=map-address-exclude-warning] input').should('not.be.checked');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '4');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(1).should('have.text', '2');
    cy.get('[data-cy=map-issue-address]').should('contain', '2');
    cy.get('[data-cy=map-address-result]').should('contain', '4').and('contain', '2');

    // The left-out rows can be downloaded with their source row and reason.
    cy.get('[data-cy=map-address-download]').click();
    cy.readFile('cypress/downloads/addr-excluded.csv', 'utf8').then((text) => {
      const lines = text.replace(/^﻿/, '').split('\r\n');
      expect(lines).to.have.length(3);
      expect(lines[1]).to.match(new RegExp(`^3,${rows[1]},`));
      expect(lines[2]).to.match(new RegExp(`^4,${rows[2]},`));
    });

    // The second switch also leaves the warning rows out; the first can be turned off.
    cy.get('[data-cy=map-address-exclude-warning] .check').click();
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '2');
    cy.get('[data-cy=map-address-exclude-warning] .check').click();
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '4');
    // The check ran once: rebuilding the upload does not ask again.
    cy.get('@importCheck.all').should('have.length', 1);

    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get('button[type=submit]').should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    cy.request('/api/import/subscribers').its('body.data.imported').should('eq', 4);
    cy.get('[data-cy=map-mismatch]').should('not.exist');
    [1, 2].forEach((n) => {
      cy.request({ url: '/api/subscribers', qs: { query: `email = '${rows[n]}'`, per_page: 1 } })
        .its('body.data.results').should('have.length', 0);
    });

    // The finished box checks the lists the server reports for this import (the status and
    // the lists come in one answer), so it is the same after a reload.
    cy.request('/api/import/subscribers').its('body.data.axm_lists').should('have.length', 1);
    cy.wait('@listCheck').its('request.body.list_ids').should('have.length', 1);
    cy.get('.status [data-cy=address-check]').should('have.class', 'is-ok');
    cy.get('.status [data-cy=address-scope]').should('contain', importListName);
    cy.reload();
    cy.get('.status [data-cy=address-scope]').should('contain', importListName);
    cy.get('.status button').first().click();
    cy.get('form.box').should('be.visible');
    cy.get('[data-cy=address-check]').should('not.exist');
    cy.request('/api/import/subscribers').its('body.data.axm_lists').should('have.length', 0);
  });

  it('checks the audience of an opt-in campaign: the unconfirmed subscribers of its double opt-in lists', () => {
    const name = `Address optin ${stamp}`;
    const pending = `optin-${stamp}@example.invalid`;
    cy.request('POST', '/api/lists', {
      name, type: 'private', optin: 'double', no_team: true,
    }).then(({ body }) => {
      const id = body.data.id;
      // Not confirmed yet: this is who an opt-in campaign mails. The check is not stubbed,
      // and DNS has no .invalid domain.
      cy.request('POST', '/api/subscribers', {
        name: 'Synthetic optin', email: pending, status: 'enabled', lists: [id],
      });
      cy.request('POST', '/api/campaigns', {
        name: `Address optin campaign ${stamp}`, subject: 'Confirm', type: 'optin', lists: [id],
      }).then(({ body: campaign }) => {
        cy.intercept('POST', listCheck).as('optinCheck');
        cy.visit(`/admin/campaigns/${campaign.data.id}`);
        cy.wait('@optinCheck').its('request.body').should('deep.equal', { list_ids: [id], optin: true });
        cy.get('[data-cy=address-check]', { timeout: 20000 }).should('have.class', 'is-risk');
        cy.get('[data-cy=address-summary]').should('contain', 'Of 1 recipients');
        cy.get('[data-cy=address-toggle]').click();
        cy.get('[data-cy=address-row-dead]').should('contain', pending);
      });
    });
  });

  it('starts every new file with the default switches, also right after an import in the same tab', () => {
    const file = (name, emails) => ({
      contents: Cypress.Buffer.from(`email,name\n${emails.map((email, n) => `${email},Synthetic ${n}`).join('\n')}\n`),
      fileName: name,
      mimeType: 'text/csv',
    });
    const first = [0, 1, 2].map((n) => `first-${stamp}-${n}@example.invalid`);
    const second = [`second-${stamp}@example.invalid`, `info+second-${stamp}@example.invalid`, `ok-second-${stamp}@example.invalid`];
    // Row 2 (the second address) of each file is dead, row 3 is a role address.
    cy.intercept('POST', importCheck, (req) => {
      req.reply({
        data: {
          total: 3, risky: 2, confirmed: 1, warning: 1, unknown: 0, pending: 0, classes: {}, issues: { 0: ['dead'], 1: ['role'] },
        },
      });
    }).as('importCheck');
    cy.intercept('POST', listCheck, { data: clean(3) }).as('listCheck');

    cy.visit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('form.box').should('be.visible');
    cy.get('.list-selector input').type(importListName);
    cy.contains('.list-selector .autocomplete a', importListName).click();
    cy.get('input[type=file]').selectFile(file('first.csv', first), { force: true });
    cy.get('[data-cy=map-busy]').should('not.exist');
    cy.get('[data-cy=map-mode-mapping]').click();
    cy.wait('@importCheck');
    // The first file: the dead row is out, and the role row too once the warning switch is on.
    cy.get('[data-cy=map-address-exclude-warning] .check').click();
    cy.get('[data-cy=map-address-exclude-warning] input').should('be.checked');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '1');
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get('button[type=submit]').should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');

    // Done, and the next file in the same tab: back to the defaults, without touching anything.
    cy.get('.status button').first().click();
    cy.get('form.box').should('be.visible');
    cy.get('input[type=file]').selectFile(file('second.csv', second), { force: true });
    cy.get('[data-cy=map-busy]').should('not.exist');
    cy.get('[data-cy=map-mode-mapping]').click();
    cy.wait('@importCheck');
    cy.get('[data-cy=map-address-exclude-warning] input').should('not.be.checked');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '2');
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get('.list-selector .tag').should('contain', importListName);
    cy.get('button[type=submit]').should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    cy.request('/api/import/subscribers').its('body.data.imported').should('eq', 2);
    const found = (email, n) => cy.request({ url: '/api/subscribers', qs: { query: `email = '${email}'`, per_page: 1 } })
      .its('body.data.results').should('have.length', n);
    found(second[0], 0);
    found(second[1], 1);
    found(second[2], 1);
    cy.get('.status button').first().click();
  });

  it('does not block the upload when the check fails, and has no check for a blocklist import', () => {
    cy.intercept('POST', importCheck, { statusCode: 500, body: { message: 'Synthetic failure' } }).as('importCheck');
    cy.visit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector input').type(importListName);
    cy.contains('.list-selector .autocomplete a', importListName).click();
    cy.get('input[type=file]').selectFile({
      contents: Cypress.Buffer.from(`email,name\nfail-${stamp}@example.invalid,Synthetic\n`), fileName: 'fail.csv', mimeType: 'text/csv',
    }, { force: true });
    // A file with the standard header starts in as-is mode, which has no review and no check.
    cy.get('[data-cy=map-mode-raw] input').should('be.checked').and('not.be.disabled');
    cy.get('[data-cy=map-busy]').should('not.exist');
    cy.get('[data-cy=map-mode-mapping]').click();
    cy.wait('@importCheck');
    cy.get('[data-cy=map-address-error]').should('be.visible');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '1');
    cy.get('button[type=submit]').should('not.be.disabled');

    cy.get('[data-cy=check-blocklist]').click();
    cy.get('[data-cy=map-address]').should('not.exist');
  });

  const openImport = (contents, fileName) => {
    cy.visit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector input').type(importListName);
    cy.contains('.list-selector .autocomplete a', importListName).click();
    cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from(contents), fileName, mimeType: 'text/csv' }, { force: true });
  };
  const found = (email, count) => cy.request({ url: '/api/subscribers', qs: { query: `email = '${email}'`, per_page: 1 } })
    .its('body.data.results').should('have.length', count);

  it('cannot be uploaded before the check has answered', () => {
    const bad = `early-${stamp}-bad@example.invalid`;
    const good = `early-${stamp}-good@example.invalid`;
    let release;
    const held = new Promise((resolve) => { release = resolve; });
    cy.intercept('POST', importCheck, (req) => held.then(() => req.reply({
      data: {
        total: 2, risky: 1, confirmed: 1, warning: 0, unknown: 0, pending: 0, classes: {}, issues: { 0: ['dead'] },
      },
    }))).as('importCheck');
    openImport(`Mail,Person\n${bad},Synthetic\n${good},Synthetic\n`, 'early.csv');
    cy.get('[data-cy=map-address-busy]').should('be.visible');
    cy.get('[data-cy=map-busy]').should('not.exist');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '2');
    cy.get('button[type=submit]').should('be.disabled');
    cy.then(() => release());
    cy.wait('@importCheck');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '1');
    cy.get('button[type=submit]').should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    found(good, 1);
    found(bad, 0);
    cy.get('.status button').first().click();
  });

  it('checks again when another column becomes the address, and leaves out the rows of that column', () => {
    // The same two addresses, one row lower in the second column.
    const bad = `shift-${stamp}-bad@example.invalid`;
    const good = `shift-${stamp}-good@example.invalid`;
    cy.intercept('POST', importCheck, (req) => req.reply({
      data: {
        total: req.body.emails.length,
        risky: 1,
        confirmed: 1,
        warning: 0,
        unknown: 0,
        pending: 0,
        classes: {},
        issues: { [req.body.emails.indexOf(bad)]: ['dead'] },
      },
    })).as('importCheck');
    openImport(`First,Second\n${bad},\n${good},${bad}\n,${good}\n`, 'shift.csv');
    cy.get('[data-cy=map-role-0]').select('email');
    cy.get('[data-cy=map-role-1]').select('discard');
    cy.wait('@importCheck');
    cy.get('[data-cy=map-address-dead]').should('contain', `2 (${bad})`);
    cy.get('[data-cy=map-preview]').should('contain', good).and('not.contain', bad);

    cy.get('[data-cy=map-role-0]').select('discard');
    cy.get('[data-cy=map-role-1]').select('email');
    cy.wait('@importCheck');
    cy.get('[data-cy=map-address-dead]').should('contain', `3 (${bad})`);
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '1');
    cy.get('[data-cy=map-preview]').should('contain', good).and('not.contain', bad);
    cy.get('button[type=submit]').should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    found(good, 1);
    found(bad, 0);
    cy.get('.status button').first().click();
  });

  it('leaves out a row that is both confirmed and a role address without a switch', () => {
    const both = `info@both-${stamp}.invalid`;
    cy.intercept('POST', importCheck, {
      data: {
        total: 2, risky: 1, confirmed: 1, warning: 0, unknown: 0, pending: 0, classes: {}, issues: { 0: ['dead', 'role'] },
      },
    }).as('importCheck');
    openImport(`Mail,Person\n${both},Synthetic\nboth-${stamp}@example.invalid,Synthetic\n`, 'both.csv');
    cy.wait('@importCheck');
    cy.get('[data-cy=map-summary] .map-counts strong').eq(0).should('have.text', '1');
    // The row is confirmed, so it is out and the warning switch has nothing to add: it is not shown.
    cy.get('[data-cy=map-address-exclude-warning]').should('not.exist');
    cy.get('[data-cy=map-address-fixed]').should('contain', '1');
    cy.get('[data-cy=map-issue-address]').should('contain', '1');
  });
});
