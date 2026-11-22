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
      expect(d.axm_rejected_total).to.eq(4);
    });
    cy.request('/api/import/subscribers/rejected').then((resp) => {
      expect(resp.headers['content-type']).to.contain('text/csv');
      expect(resp.body).to.contain('gate-typo-a@gmail.con').and.contain('gate-typo-a@gmail.com');
    });
    cy.get('[data-cy=import-rejected-download]').should('have.attr', 'href', '/api/import/subscribers/rejected');
  });
});
