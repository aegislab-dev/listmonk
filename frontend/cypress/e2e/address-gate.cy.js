// LST-53 step 1: the address rules (misspelled domains, Gmail mailbox names) at the places a
// subscriber is added. All data is synthetic; no DNS is used (the address check is stubbed).
const submit = () => cy.get('button[type=submit]');
let listName;

describe('Address rules', () => {
  before(() => {
    cy.loginAndVisit('/admin/subscribers/import');
    listName = `Gate synthetic ${Date.now()}`;
    cy.request('POST', '/api/lists', { name: listName, type: 'private', optin: 'single', tags: [] });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
  });

  it('refuses a misspelled domain on a single add, offers the fix, and saves it only when asked', () => {
    cy.loginAndVisit('/admin/subscribers');
    cy.get('[data-cy=btn-new]').click();
    cy.get('input[name=email]').type('gatelee@gmail.con');
    cy.get('input[name=name]').type('Gate Lee');
    cy.get('.modal-card-foot button[type=submit]').click();
    cy.get('.modal-card').should('be.visible').and('contain', 'gatelee@gmail.com');
    cy.get('[data-cy=addr-suggest-apply]').should('contain', 'gmail.com');
    cy.get('input[name=email]').should('have.value', 'gatelee@gmail.con');
    cy.get('[data-cy=addr-suggest-apply]').click();
    cy.get('input[name=email]').should('have.value', 'gatelee@gmail.com');
    cy.get('[data-cy=addr-suggest]').should('not.exist');
    cy.get('.modal-card-foot button[type=submit]').click();
    cy.get('.modal-card').should('not.exist');
    cy.request({ url: '/api/subscribers', qs: { query: "email = 'gatelee@gmail.com'" } })
      .its('body.data.results').should('have.length', 1);
  });

  it('keeps a name outside ASCII whole when the domain fix is applied', () => {
    const stamp = Date.now();
    ['리뷰', 'réview'].forEach((name) => {
      cy.loginAndVisit('/admin/subscribers');
      cy.get('[data-cy=btn-new]').click();
      cy.get('input[name=email]').type(`${name}${stamp}@dept.example.con`);
      cy.get('input[name=name]').type('Gate Intl');
      cy.get('.modal-card-foot button[type=submit]').click();
      cy.get('[data-cy=addr-suggest-apply]').click();
      cy.get('input[name=email]').should('have.value', `${name}${stamp}@dept.example.com`);
      cy.get('.modal-card-foot button[type=submit]').click();
      cy.get('.modal-card').should('not.exist');
      cy.request({ url: '/api/subscribers', qs: { query: `email = '${name}${stamp}@dept.example.com'` } })
        .its('body.data.results').should('have.length', 1);
    });
  });

  it('refuses a Gmail name that cannot exist, with no suggestion button', () => {
    cy.loginAndVisit('/admin/subscribers');
    cy.get('[data-cy=btn-new]').click();
    cy.get('input[name=email]').type('abc@gmail.com');
    cy.get('input[name=name]').type('Gate Short');
    cy.get('.modal-card-foot button[type=submit]').click();
    cy.get('.modal-card').should('contain', 'Gmail');
    cy.get('[data-cy=addr-suggest]').should('not.exist');
    cy.request({ url: '/api/subscribers', qs: { query: "email = 'abc@gmail.com'" } })
      .its('body.data.results').should('have.length', 0);
  });

  it('the API answers 400 and the header carries the suggestion', () => {
    cy.request({
      method: 'POST', url: '/api/subscribers', failOnStatusCode: false,
      body: { email: 'gate-api@gmaii.com', name: 'Gate Api', status: 'enabled', lists: [] },
    }).then((resp) => {
      expect(resp.status).to.eq(400);
      expect(resp.headers['x-axm-suggest']).to.eq('gate-api@gmail.com');
    });
  });

  it('leaves the refused rows of an import out, counts them by reason and offers them as CSV', () => {
    cy.intercept('POST', '/api/subscribers/address-check', (req) => req.reply({
      data: {
        total: req.body.emails.length, risky: 0, confirmed: 0, warning: 0, unknown: 0, pending: 0, classes: {}, issues: {}, suggest: {},
      },
    }));
    cy.loginAndVisit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector input').type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    const csv = 'email,name\n'
      + 'gate-good-one@example.invalid,Good One\n'
      + 'gate-typo-a@gmail.con,Typo A\n'
      + 'gate-typo-b@gmaii.com,Typo B\n'
      + 'abc@gmail.com,Short\n'
      + 'gate_under@gmail.com,Underscore\n'
      + 'gate-good-two@example.invalid,Good Two\n';
    cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from(csv), fileName: 'gate.csv', mimeType: 'text/csv' }, { force: true });
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    submit().should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    cy.get('[data-cy=import-rejected]').should('contain', '4');
    cy.get('[data-cy=import-rejected-typo]').should('contain', '2');
    cy.get('[data-cy=import-rejected-provider]').should('contain', '2');
    cy.get('[data-cy=map-mismatch]').should('not.exist');
    cy.request('/api/import/subscribers').its('body.data').should((d) => {
      expect(d.imported).to.eq(2);
      expect(d.total).to.eq(6);
      expect(d.axm_rejected_total).to.eq(4);
    });
    cy.request('/api/import/subscribers').its('body.data.axm_import_id').then((id) => cy.request(`/api/import/subscribers/rejected?id=${id}`)).then((resp) => {
      expect(resp.headers['content-type']).to.contain('text/csv');
      expect(resp.body).to.contain('gate-typo-a@gmail.con').and.contain('gate-typo-a@gmail.com');
    });
    cy.get('[data-cy=import-rejected-download]').should('have.attr', 'href').and('match', /^\/api\/import\/subscribers\/rejected\?id=\d+$/);
  });

  it('shows the rows the column mapping left out in the box that ends the import, and the server rows by the import number', () => {
    const stamp = Date.now();
    cy.intercept('POST', '/api/subscribers/address-check', (req) => req.reply({
      data: {
        total: req.body.emails.length,
        risky: 3,
        confirmed: 3,
        warning: 0,
        unknown: 0,
        pending: 0,
        classes: {},
        issues: { 1: ['typo'], 2: ['provider'], 3: ['dead'] },
        suggest: { 1: `map-typo-${stamp}@gmail.com` },
      },
    }));
    cy.loginAndVisit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector input').type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    const csv = 'email,name\n'
      + `map-good-${stamp}@example.invalid,Good\n`
      + `map-typo-${stamp}@gmail.con,Typo\n`
      + 'abc@gmail.com,Short\n'
      + `map-dead-${stamp}@example.invalid,Dead\n`
      + 'not-an-address,Broken\n'
      + ',Nameonly\n';
    cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from(csv), fileName: 'mapped.csv', mimeType: 'text/csv' }, { force: true });
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    // A file with the standard header starts in as-is mode: ask for the review.
    cy.get('[data-cy=map-mode-mapping]').click();
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    // The preview names the corrected address and says what the Gmail rule is; no switch for these rows.
    cy.get('[data-cy=map-address-typo]').should('contain', `map-typo-${stamp}@gmail.com`);
    cy.get('[data-cy=map-address-provider]').should('contain', '6');
    cy.get('[data-cy=map-address-exclude-confirmed]').should('not.exist');
    cy.get('[data-cy=map-address-fixed]').should('contain', '3');
    cy.get('.map-issues').should('contain', 'not-an-address');
    submit().should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    // The box counts the three left out rows by reason and keeps their CSV; nothing is refused by the server.
    cy.get('[data-cy=import-rejected]').should('contain', '5');
    cy.get('[data-cy=import-rejected-format]').should('contain', '2');
    cy.get('[data-cy=import-rejected-typo]').should('contain', '1');
    cy.get('[data-cy=import-rejected-provider]').should('contain', '1');
    cy.get('[data-cy=import-rejected-dead]').should('contain', '1');
    cy.get('[data-cy=import-rejected-download]').should('exist');
    cy.request('/api/import/subscribers').its('body.data').should((d) => {
      expect(d.axm_excluded).to.eq(5);
      expect(d.axm_rejected_total).to.eq(5);
      // The total counts the left out rows, as the raw upload of the file does.
      expect(d.total).to.eq(6);
    });
    cy.get('[data-cy=map-mismatch]').should('not.exist');
    // The box and its CSV are the server's: a reload keeps them, with the rows of the file the person chose.
    cy.reload();
    cy.get('[data-cy=import-rejected]').should('contain', '5');
    cy.request('/api/import/subscribers').its('body.data.axm_import_id').then((id) => cy.request(`/api/import/subscribers/rejected?id=${id}`))
      .its('body').should('contain', '\n3,').and('contain', '\n4,').and('contain', '\n5,').and('contain', '\n6,not-an-address').and('contain', '\n7,');
  });

  it('the review screen download holds the malformed rows too, also when no row is left to import', () => {
    const stamp = Date.now();
    cy.intercept('POST', '/api/subscribers/address-check', (req) => req.reply({
      data: {
        total: req.body.emails.length,
        risky: 2,
        confirmed: 2,
        warning: 0,
        unknown: 0,
        pending: 0,
        classes: {},
        issues: { 0: ['typo'], 1: ['provider'] },
        suggest: { 0: `all-typo-${stamp}@gmail.com` },
      },
    }));
    cy.loginAndVisit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector input').type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    const csv = `email,name\nall-typo-${stamp}@gmail.con,Typo\nabc@gmail.com,Short\nnot-an-address,Broken\n`;
    cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from(csv), fileName: 'allbad.csv', mimeType: 'text/csv' }, { force: true });
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get('[data-cy=map-mode-mapping]').click();
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get('[data-cy=map-address-fixed]').should('contain', '2');
    submit().should('be.disabled');
    cy.get('[data-cy=map-address-download]').click();
    cy.readFile('cypress/downloads/allbad-excluded.csv', 'utf8').then((text) => {
      const lines = text.replace(/^\uFEFF/, '').split('\r\n').filter(Boolean);
      expect(lines.slice(1).map((line) => Number(line.split(',')[0]))).to.deep.equal([2, 3, 4]);
      expect(lines[3]).to.match(/^4,not-an-address,/);
    });
  });

  // The same file gives the same refused rows, numbered alike, whether it goes as it is or
  // through the column mapping.
  const refusedRows = (csv, mode) => {
    cy.intercept('POST', '/api/subscribers/address-check', (req) => req.reply({
      data: {
        total: req.body.emails.length, risky: 0, confirmed: 0, warning: 0, unknown: 0, pending: 0, classes: {}, issues: {}, suggest: {},
      },
    }));
    cy.loginAndVisit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector input').type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from(csv), fileName: `${mode}.csv`, mimeType: 'text/csv' }, { force: true });
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get(`[data-cy=map-mode-${mode}]`).click();
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    submit().should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    return cy.request('/api/import/subscribers').its('body.data.axm_import_id')
      .then((id) => cy.request(`/api/import/subscribers/rejected?id=${id}`))
      .then(({ body }) => body.split(/\r?\n/).slice(1).filter(Boolean).map((line) => Number(line.split(',')[0])).sort((a, b) => a - b));
  };

  it('counts a quoted empty address in a one-column file in both upload modes', () => {
    const stamp = Date.now();
    // 2 address, 3 "" (an empty address), 4 blank line, 5 misspelled domain.
    const csv = `email\none-col-${stamp}@example.invalid\n""\n\none-col-${stamp}@gmail.con\n`;
    refusedRows(csv, 'raw').should('deep.equal', [3, 5]);
    cy.get('[data-cy=import-rejected]').should('contain', '2');
    refusedRows(csv, 'mapping').should('deep.equal', [3, 5]);
    cy.get('[data-cy=import-rejected]').should('contain', '2');
  });

  it('numbers the rows after a wrong-width row with a line break alike in both upload modes', () => {
    const stamp = Date.now();
    // 2 address, 3 a two-line name and an extra column (skipped), 4 misspelled domain.
    const csv = `email,name\nwide-${stamp}@example.invalid,A\nwide-b-${stamp}@example.invalid,"two\nlines",extra\nwide-${stamp}@gmail.con,D\n`;
    refusedRows(csv, 'raw').should('deep.equal', [4]);
    refusedRows(csv, 'mapping').should('deep.equal', [4]);
  });

  it('gives the same total in both upload modes when cells hold line breaks', () => {
    const stamp = Date.now();
    const csv = `email,name\nlb-good-${stamp}@example.invalid,"Good\nOne"\nlb-typo-${stamp}@gmail.con,"Typo\nTwo"\nabc@gmail.com,Short\n`;
    ['raw', 'mapping'].forEach((mode) => {
      refusedRows(csv, mode).should('deep.equal', [3, 4]);
      cy.request('/api/import/subscribers').its('body.data').should((d) => {
        expect(d.total).to.eq(3);
        expect(d.axm_rejected_total).to.eq(2);
      });
      cy.get('section.status').should('contain', '1 / 3');
    });
  });

  it('offers the left out rows when only malformed and empty addresses are left out', () => {
    cy.loginAndVisit('/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector input').type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    const csv = 'email,name\nnot-an-address,Broken\n,Nameonly\n';
    cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from(csv), fileName: 'onlybad.csv', mimeType: 'text/csv' }, { force: true });
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    cy.get('[data-cy=map-mode-mapping]').click();
    cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
    submit().should('be.disabled');
    cy.get('[data-cy=map-address-download]').should('not.exist');
    cy.get('[data-cy=map-issues-download]').click();
    cy.readFile('cypress/downloads/onlybad-excluded.csv', 'utf8').then((text) => {
      const lines = text.replace(/^﻿/, '').split('\r\n').filter(Boolean);
      expect(lines.slice(1).map((line) => Number(line.split(',')[0]))).to.deep.equal([2, 3]);
    });
  });
});
