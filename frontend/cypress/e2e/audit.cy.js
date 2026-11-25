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
    // The page says what is recorded and, in a second line, what is not.
    cy.get('[data-cy=audit-help]').should('contain', 'Only these actions are recorded').and('contain', 'test sends');
    cy.get('[data-cy=audit-not-logged]').should('contain', 'Not recorded').and('contain', 'editing subscribers').and('contain', 'sender addresses');
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
    // Retries until the filtered page is there: no list action is left.
    cy.get('[data-cy=audit-action-label]').should(($els) => {
      expect($els.length).to.be.greaterThan(0);
      $els.each((_, el) => expect(el.textContent).to.not.match(/(Created|Edited|Deleted|Restored|Changed) list|Trash/));
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

  it('counts what a bulk action by search reached, not every subscriber', () => {
    ['a', 'b', 'c'].forEach((n) => cy.request('POST', '/api/subscribers', {
      email: `bulk-${stamp}-${n}@example.invalid`, name: `대량시험${stamp}${n}`, status: 'enabled', lists: [],
    }));
    cy.request('POST', '/api/subscribers/query/delete', { search: `대량시험${stamp}a`, list_ids: [] });
    cy.request('/api/audit?per_page=all&action=subscriber.delete').then(({ body }) => {
      const row = body.data.results.filter((r) => r.detail.by === 'query' && r.detail.search).pop();
      expect(row.count).to.eq(1);
      expect(JSON.stringify(row)).to.not.contain('@');
    });
    cy.request('/api/subscribers?per_page=all&query=' + encodeURIComponent(`subscribers.name LIKE '대량시험${stamp}%'`)).then(({ body }) => {
      body.data.results.forEach((sub) => cy.request('DELETE', `/api/subscribers/${sub.id}`));
    });
  });

  it('records a change of a user role with both role names', () => {
    cy.request('POST', '/api/roles/users', { name: `기록역할${stamp}`, permissions: ['subscribers:get'] }).then(({ body: role }) => {
      cy.request('POST', '/api/users', {
        username: `rec${stamp}`, type: 'user', name: '기록 사용자', email: `rec-${stamp}@example.invalid`, status: 'enabled',
        password_login: true, password: 'record-pass-1234', user_role_id: role.data.id,
      }).then(({ body: user }) => {
        cy.request('PUT', `/api/users/${user.data.id}`, {
          username: `rec${stamp}`, type: 'user', name: '기록 사용자', email: `rec-${stamp}@example.invalid`, status: 'enabled',
          password_login: true, user_role_id: 1,
        });
        cy.request('/api/audit?per_page=all&area=access').then(({ body }) => {
          const rows = body.data.results.filter((r) => r.target_name === `rec${stamp}`);
          const created = rows.find((r) => r.action === 'user.create');
          const updated = rows.find((r) => r.action === 'user.update');
          expect(created.detail.user_role).to.eq(`기록역할${stamp}`);
          expect(updated.detail.user_role.from).to.eq(`기록역할${stamp}`);
          expect(updated.detail.user_role.to).to.eq('Super Admin');
          expect(JSON.stringify(rows)).to.not.contain('@');
        });
        cy.request('DELETE', `/api/users/${user.data.id}`);
        cy.request('DELETE', `/api/roles/${role.data.id}`);
      });
    });
  });
});
