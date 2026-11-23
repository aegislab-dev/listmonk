// LST-55: a campaign that is sending (running, paused) is not deleted from the list, one that has
// sent mail is refused by the server (409), and a delete writes its status and counts to the
// activity log. CI has no mail server, so a real send is covered by the Go DB tests: here the
// list response is rewritten to show a running campaign and the 409 is stubbed.
const stamp = Date.now();
const keep = `Delete guard keep ${stamp}`;
const gone = `Delete guard gone ${stamp}`;
let listId;
let keepId;

const rowOf = (name) => cy.contains('tbody tr', name);
const search = (name) => {
  cy.get('input[name=query]').clear().type(`${name}{enter}`);
  rowOf(name).should('exist');
};

describe('Campaign delete guard', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', {
      name: `Delete guard list ${stamp}`, type: 'private', optin: 'single', no_team: true,
    }).then(({ body }) => {
      listId = body.data.id;
      cy.request('POST', '/api/campaigns', { name: keep, subject: 'Keep', type: 'regular', lists: [listId] })
        .then(({ body: c }) => { keepId = c.data.id; });
      cy.request('POST', '/api/campaigns', { name: gone, subject: 'Gone', type: 'regular', lists: [listId] });
    });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
    cy.intercept('DELETE', '/api/campaigns*', cy.spy().as('deleteSpy')).as('deleteCall');
  });

  it('explains instead of asking for a running campaign, and sends no request', () => {
    cy.intercept('GET', '/api/campaigns?*', (req) => {
      req.continue((res) => {
        res.body.data.results.forEach((c) => { if (c.name === keep) { c.status = 'running'; } });
      });
    }).as('rewritten');
    cy.visit('/admin/campaigns');
    cy.wait('@rewritten');
    search(keep);
    rowOf(keep).find('[data-cy=btn-delete]').click();
    cy.get('.modal [data-cy=warn-box]').should('contain', keep).and('contain', 'Running');
    cy.get('.modal-card-title').should('contain', 'cannot be deleted');
    cy.get('.modal button.is-primary').click();
    cy.get('.modal').should('not.exist');
    cy.get('@deleteSpy').should('not.have.been.called');
  });

  it('shows the server reason when a delete is refused (single and bulk)', () => {
    const reason = 'Server says: this campaign has sent mail.';
    cy.intercept('DELETE', '/api/campaigns/*', { statusCode: 409, body: { message: reason } }).as('refuseOne');
    cy.visit('/admin/campaigns');
    search(keep);
    rowOf(keep).find('[data-cy=btn-delete]').click();
    cy.get('.modal button.is-primary').click(); // the usual confirmation
    cy.wait('@refuseOne');
    cy.get('.modal [data-cy=warn-box]').should('contain', reason);
    cy.get('.modal-card-title').should('contain', 'cannot be deleted');
    cy.get('.modal button.is-primary').click();

    const many = 'Server says: 2 of the selected campaigns have sent mail.';
    cy.intercept('DELETE', '/api/campaigns?*', { statusCode: 409, body: { message: many } }).as('refuseMany');
    search(keep);
    cy.get('thead input[type="checkbox"]').click({ force: true });
    cy.get('a[data-cy=btn-delete-campaigns]').click();
    cy.get('.modal button.is-primary').click();
    cy.wait('@refuseMany');
    cy.get('.modal [data-cy=warn-box]').should('contain', many);
    cy.get('.modal-card-title').should('contain', 'Some campaigns cannot be deleted');
    cy.get('.modal button.is-primary').click();
    // The selection is kept and nothing was removed.
    cy.get('a[data-cy=btn-delete-campaigns]').should('exist');
    rowOf(keep).should('exist');
  });

  it('still deletes a draft, and the activity log says what it was', () => {
    cy.visit('/admin/campaigns');
    search(gone);
    rowOf(gone).find('[data-cy=btn-delete]').click();
    cy.get('.modal button.is-primary').click();
    cy.wait('@deleteCall').its('response.statusCode').should('eq', 200);
    cy.contains('tbody tr', gone).should('not.exist');

    cy.request('/api/audit?per_page=all&area=campaign').then(({ body }) => {
      const row = body.data.results.find((r) => r.action === 'campaign.delete' && r.target_name === gone);
      expect(row, 'delete row').to.exist;
      expect(row.count).to.eq(0);
      expect(row.detail.status).to.eq('draft');
      expect(row.detail.sent).to.eq(0);
      expect(row.detail).to.have.property('to_send');
    });

    cy.visit('/admin/settings/audit');
    cy.get('[data-cy=audit-query]').type(gone);
    cy.get('[data-cy=btn-audit-query]').click();
    cy.get('tbody tr:not(.detail) .chevron-cell a').first().click();
    cy.get('[data-cy=audit-detail]').should('contain', 'Status').and('contain', 'draft')
      .and('contain', 'Sent').and('contain', 'To send');
  });

  after(() => {
    // The campaign that was kept (a draft) and its list go with the rest of the data.
    cy.request({ method: 'DELETE', url: `/api/campaigns/${keepId}`, failOnStatusCode: false });
    cy.request({ method: 'DELETE', url: `/api/lists/${listId}`, failOnStatusCode: false });
  });
});
