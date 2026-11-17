// Disposable synthetic-data coverage for the activity log (LST-27): the actions of this spec
// show up with who did them, the filters work, and no subscriber address is ever written.
const stamp = Date.now();
const listName = `기록시험${stamp}`;
const renamed = `기록시험개명${stamp}`;
const email = `audit-${stamp}@example.invalid`;
let listId;

describe('activity log', () => {
  before(() => {
    cy.loginAndVisit('/admin/settings/audit');
    cy.request('POST', '/api/lists', { name: listName, type: 'private', optin: 'single', no_team: true })
      .then(({ body }) => { listId = body.data.id; });
    cy.then(() => cy.request('PUT', `/api/lists/${listId}`, { name: renamed, type: 'private', optin: 'single', no_team: true }));
    cy.request('POST', '/api/subscribers', { email, name: '기록 구독자', status: 'enabled', lists: [] });
    // A bulk delete by a condition that holds the address.
    cy.request('POST', '/api/subscribers/query/delete', { query: `subscribers.email = '${email}'`, list_ids: [] });
    cy.then(() => cy.request('DELETE', `/api/lists/${listId}`));
  });

  beforeEach(() => { cy.viewport(1440, 1000); });

  it('is in the settings menu and lists what was done, newest first', () => {
    cy.visit('/admin/settings/audit');
    cy.get('[data-cy=audit-query]').type(renamed);
    cy.get('[data-cy=btn-audit-query]').click();
    cy.get('tbody tr:not(.detail)').should('have.length.at.least', 2);
    cy.get('[data-cy=audit-action-label]').first().should('contain', 'Deleted list');
    cy.get('[data-cy=audit-who]').first().should('not.be.empty');
    cy.get('[data-cy=audit-count]').first().should('contain', '0');
  });

  it('shows the details of a row and filters by area and action', () => {
    cy.get('tbody tr:not(.detail) .chevron-cell a').last().click();
    cy.get('[data-cy=audit-detail]').should('exist');
    cy.get('[data-cy=audit-reset]').click();
    cy.get('[data-cy=audit-area]').select('Subscribers');
    cy.get('[data-cy=audit-action-label]').each(($el) => {
      expect($el.text()).to.match(/subscriber|Blocklisted|Added|Removed|Unsubscribed|Cleaned/i);
    });
    cy.get('[data-cy=audit-action]').select('Deleted subscribers');
    cy.get('[data-cy=audit-action-label]').first().should('contain', 'Deleted subscribers');
  });

  it('never holds a subscriber address', () => {
    cy.request('/api/audit?per_page=all&area=subscriber').then(({ body }) => {
      const text = JSON.stringify(body);
      expect(text).to.not.contain('@');
      expect(text).to.not.contain(email);
      expect(body.data.results.some((r) => r.action === 'subscriber.delete')).to.eq(true);
    });
  });

  it('is only read through the API', () => {
    cy.request({ method: 'DELETE', url: '/api/audit', failOnStatusCode: false }).its('status').should('be.oneOf', [404, 405]);
    cy.request({ method: 'PUT', url: '/api/audit/1', body: {}, failOnStatusCode: false }).its('status').should('be.oneOf', [404, 405]);
  });
});
