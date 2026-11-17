// Disposable synthetic-data coverage for the list delete protection (LST-27): the dialog that
// asks for the name, the trash and the restore. No campaign is sent.
const stamp = Date.now();
const nameA = `휴지통시험A${stamp}`;
const nameB = `휴지통시험B${stamp}`;
let idA;
let idB;
let subId;

describe('list trash', () => {
  before(() => {
    cy.loginAndVisit('/admin/lists');
    cy.request('POST', '/api/lists', { name: nameA, type: 'private', optin: 'single', no_team: true })
      .then(({ body }) => { idA = body.data.id; });
    cy.request('POST', '/api/lists', { name: nameB, type: 'private', optin: 'single', no_team: true })
      .then(({ body }) => { idB = body.data.id; });
    cy.request('POST', '/api/subscribers', {
      email: `trash-${stamp}@example.invalid`, name: '휴지통 구독자', status: 'enabled', lists: [], preconfirm_subscriptions: true,
    }).then(({ body }) => {
      subId = body.data.id;
      cy.request('PUT', '/api/subscribers/lists', { ids: [subId], action: 'add', target_list_ids: [idA], status: 'confirmed' });
    });
  });

  beforeEach(() => { cy.viewport(1440, 1000); });

  after(() => {
    cy.request('DELETE', `/api/subscribers/${subId}`);
    cy.request('/api/lists?per_page=all').then(({ body }) => {
      body.data.results.filter((l) => l.name.endsWith(`${stamp}`)).forEach((l) => cy.request('DELETE', `/api/lists/${l.id}`));
    });
  });

  it('asks for the list name before the delete button works', () => {
    cy.visit('/admin/lists');
    cy.contains('tbody tr', nameA).find('[data-cy=btn-delete]').click();
    cy.get('[data-cy=list-delete-confirm]').should('contain', nameA).and('contain', '14');
    cy.get('[data-cy=btn-delete-confirm]').should('be.disabled');
    cy.get('[data-cy=delete-confirm-input]').type('wrong name');
    cy.get('[data-cy=btn-delete-confirm]').should('be.disabled');
    cy.get('[data-cy=delete-confirm-input]').clear().type(nameA, { parseSpecialCharSequences: false });
    cy.get('[data-cy=btn-delete-confirm]').should('not.be.disabled').click();
    cy.contains('tbody tr', nameA).should('not.exist');
  });

  it('shows the deleted list in the trash and restores it with its subscriptions', () => {
    cy.get('[data-cy=trash-link]').click();
    cy.location('pathname').should('contain', '/lists/trash');
    cy.get('[data-cy=trash-help]').should('contain', '14');
    cy.contains('tbody tr', nameA).within(() => {
      cy.get('[data-cy=trash-days-left]').should('contain', '14');
      cy.get('[data-cy=trash-by]').should('not.be.empty');
      cy.get('[data-cy=btn-restore]').click();
    });
    cy.get('.modal button.is-primary').click();
    cy.contains('tbody tr', nameA).should('not.exist');

    cy.visit('/admin/lists');
    cy.contains('tbody tr', nameA).find('td[data-label=Subscribers]').should('contain', '1');
    cy.request(`/api/subscribers/${subId}`).then(({ body }) => {
      expect(body.data.lists.map((l) => l.id)).to.include(idA);
    });
  });

  it('asks for the number when several lists are deleted', () => {
    cy.visit('/admin/lists');
    cy.contains('tbody tr', nameA).find('.b-checkbox').first().click();
    cy.contains('tbody tr', nameB).find('.b-checkbox').first().click();
    cy.get('a[data-cy=btn-delete-lists]').click();
    cy.get('[data-cy=list-delete-confirm]').should('contain', nameA).and('contain', nameB);
    cy.get('[data-cy=btn-delete-confirm]').should('be.disabled');
    cy.get('[data-cy=delete-confirm-input]').type('3');
    cy.get('[data-cy=btn-delete-confirm]').should('be.disabled');
    cy.get('[data-cy=delete-confirm-input]').clear().type('2');
    cy.get('[data-cy=btn-delete-confirm]').click();
    cy.contains('tbody tr', nameB).should('not.exist');
    cy.request('/api/lists/trash').then(({ body }) => {
      const names = body.data.results.map((l) => l.name);
      expect(names).to.include(nameA);
      expect(names).to.include(nameB);
    });
  });

  it('refuses to delete the list of a running campaign through the API', () => {
    cy.request('POST', '/api/lists', { name: `휴지통시험C${stamp}`, type: 'private', optin: 'single', no_team: true }).then(({ body }) => {
      const id = body.data.id;
      cy.request('POST', '/api/campaigns', {
        name: `휴지통 캠페인 ${stamp}`, subject: 's', lists: [id], type: 'regular', content_type: 'richtext', body: '<p>x</p>', messenger: 'email',
      }).then(({ body: camp }) => {
        // A draft campaign does not block; scheduling one does.
        cy.request('PUT', `/api/campaigns/${camp.data.id}`, {
          name: `휴지통 캠페인 ${stamp}`, subject: 's', lists: [id], type: 'regular', content_type: 'richtext', body: '<p>x</p>', messenger: 'email',
          send_at: new Date(Date.now() + 86400000).toISOString(),
        });
        cy.request('PUT', `/api/campaigns/${camp.data.id}/status`, { status: 'scheduled' });
        cy.request({ method: 'DELETE', url: `/api/lists/${id}`, failOnStatusCode: false }).its('status').should('eq', 409);
        cy.request('PUT', `/api/campaigns/${camp.data.id}/status`, { status: 'draft' });
        cy.request('DELETE', `/api/campaigns/${camp.data.id}`);
        cy.request('DELETE', `/api/lists/${id}`).its('status').should('eq', 200);
      });
    });
  });
});
