// Disposable synthetic-data coverage for the list delete protection (LST-27): the dialog that
// asks for the name, the trash and the restore. No campaign is sent.
const stamp = Date.now();
const nameA = `휴지통시험A${stamp}`;
const nameB = `휴지통시험B${stamp}`;
let idA;
let idB;
let subId;

// Earlier specs can fill the first page. Search for the lists owned by this spec.
function openFixtureLists() {
  cy.visit('/admin/lists');
  cy.get('[data-cy=query]').type(`${stamp}`);
  cy.get('[data-cy=btn-query]').click();
}

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
    openFixtureLists();
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

    openFixtureLists();
    cy.contains('tbody tr', nameA).find('td[data-label=Subscribers]').should('contain', '1');
    cy.request(`/api/subscribers/${subId}`).then(({ body }) => {
      expect(body.data.lists.map((l) => l.id)).to.include(idA);
    });
  });

  it('asks for the number when several lists are deleted', () => {
    openFixtureLists();
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

  // More lists than a page holds, so that "select all" appears and the delete goes by search,
  // not by ID: the dialog must come from GET /api/lists/delete-preview.
  it('asks for the number the select-all delete will delete, by a search', () => {
    const total = 22;
    const prefix = `휴지통전체${stamp}`;
    for (let n = 1; n <= total; n += 1) {
      cy.request('POST', '/api/lists', { name: `${prefix}-${n}`, type: 'private', optin: 'single', no_team: true });
    }
    cy.intercept('GET', '/api/lists/delete-preview*').as('preview');
    cy.intercept('DELETE', '/api/lists*').as('deleteLists');
    cy.visit('/admin/lists');
    cy.get('[data-cy=query]').type(prefix);
    cy.get('[data-cy=btn-query]').click();
    cy.get('tbody tr').should('have.length.at.most', 20);
    cy.get('thead .b-checkbox').first().click();
    cy.get('a[data-cy=select-all-lists]').should('exist').click();
    cy.get('a[data-cy=btn-delete-lists]').click();
    cy.wait('@preview').its('request.url').should('contain', 'delete-preview');
    cy.get('[data-cy=list-delete-confirm]').should('contain', `${total}`);
    cy.get('[data-cy=delete-confirm-input]').type(`${total - 1}`);
    cy.get('[data-cy=btn-delete-confirm]').should('be.disabled');
    cy.get('[data-cy=delete-confirm-input]').clear().type(`${total}`);
    cy.get('[data-cy=btn-delete-confirm]').click();
    // The delete went by search (no id in the request), and took exactly those lists.
    cy.wait('@deleteLists').its('request.url').should('not.contain', 'id=');
    cy.request('/api/lists/trash').then(({ body }) => {
      const trashed = body.data.results.map((l) => l.name).filter((n) => n.startsWith(prefix));
      expect(trashed).to.have.length(total);
    });
    cy.request('/api/lists?per_page=all').then(({ body }) => {
      expect(body.data.results.filter((l) => l.name.startsWith(prefix))).to.have.length(0);
    });
  });
});
