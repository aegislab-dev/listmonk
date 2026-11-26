// LST-70: "select all, delete" on a bounces screen that is filtered by campaign / source must
// carry the filter to the server, so it deletes what the screen shows and not every bounce.
// The list is stubbed (more rows than a page, so the "select all" link shows); the delete is
// stubbed too and only its query is read. The server side is covered by the Go DB test.
const rows = Array.from({ length: 5 }, (_, i) => ({
  id: i + 1,
  type: 'soft',
  source: 'ses',
  meta: {},
  created_at: '2026-11-20T00:00:00Z',
  subscriber_id: 100 + i,
  subscriber_uuid: `00000000-0000-0000-0000-00000000000${i}`,
  email: `bounce-filter-${i}@example.invalid`,
  subscriber_status: 'enabled',
  campaign: { id: 7, name: 'Filtered campaign' },
}));

const stubList = () => cy.intercept('GET', /\/api\/bounces\?/, {
  data: { results: rows, total: 25, per_page: 5, page: 1 },
}).as('list');

const selectAllAndDelete = ({ confirm = true } = {}) => {
  cy.wait('@list');
  cy.get('thead .b-checkbox').first().click();
  cy.get('.actions a').contains('25').click();
  cy.get('a[data-cy=btn-delete]').first().click();
  if (confirm) {
    cy.get('.modal.is-active button.is-primary').last().click();
  }
};

describe('Bounces: delete all inside the filter', () => {
  beforeEach(() => {
    cy.viewport(1440, 1000);
    cy.intercept('DELETE', /\/api\/bounces/, { data: true }).as('del');
  });

  it('sends the campaign and the source with all=true', () => {
    stubList();
    // loginAndVisit puts the path in ?next= unencoded: a second parameter would be lost.
    cy.loginAndVisit(encodeURIComponent('/admin/subscribers/bounces?campaign_id=7&source=ses'));
    selectAllAndDelete();
    cy.wait('@del').its('request.query').should('deep.equal', { all: 'true', campaign_id: '7', source: 'ses' });
  });

  it('sends only the filter that is set', () => {
    stubList();
    cy.loginAndVisit('/admin/subscribers/bounces?source=ses');
    selectAllAndDelete();
    cy.wait('@del').its('request.query').should('deep.equal', { all: 'true', source: 'ses' });
  });

  // The address is read as written: a value the screen cannot read is not dropped (that would
  // delete every campaign's bounces), it goes to the server, which refuses it.
  [['campaign_id=abc', { all: 'true', campaign_id: 'abc' }],
    ['campaign_id=', { all: 'true', campaign_id: '' }],
    ['campaign_id=abc&source=ses', { all: 'true', campaign_id: 'abc', source: 'ses' }],
  ].forEach(([qs, want]) => {
    it(`passes an unreadable filter on: ?${qs}`, () => {
      stubList();
      cy.loginAndVisit(encodeURIComponent(`/admin/subscribers/bounces?${qs}`));
      selectAllAndDelete();
      cy.wait('@del').its('request.query').should('deep.equal', want);
    });
  });

  // The confirm dialog outlives the screen: browser back leaves it open on another list. What
  // it deletes is what was selected when it opened, filter included.
  it('keeps the filter of the selection when back is pressed behind the open dialog', () => {
    stubList();
    cy.loginAndVisit('/admin/subscribers/bounces');
    cy.wait('@list');
    cy.get('tbody a').contains('ses').first().click();
    cy.location('search').should('contain', 'source=ses');
    selectAllAndDelete({ confirm: false });
    cy.go('back');
    cy.location('search').should('eq', '');
    cy.get('.modal.is-active button.is-primary').last().click();
    cy.wait('@del').its('request.query').should('deep.equal', { all: 'true', source: 'ses' });
  });

  it('sends no filter when the screen is not filtered', () => {
    stubList();
    cy.loginAndVisit('/admin/subscribers/bounces');
    selectAllAndDelete();
    cy.wait('@del').its('request.query').should('deep.equal', { all: 'true' });
  });

  // LST-73: without lists:manage_all the server leaves out the bounces of people who are on
  // other teams' lists too and answers { done, skipped }: the screen says so instead of
  // "deleted".
  it('says how many bounce records were left out as other teams\'', () => {
    cy.intercept('DELETE', /\/api\/bounces/, { data: { done: 2, skipped: 3 } }).as('del');
    stubList();
    cy.loginAndVisit('/admin/subscribers/bounces?source=ses');
    selectAllAndDelete();
    cy.wait('@del');
    cy.get('.modal.is-active').should('contain', 'Some bounce records were left out')
      .and('contain', '3 bounce records belong to people who are also on other teams');
  });

  it('keeps the plain "deleted" message when the server answers true', () => {
    stubList();
    cy.loginAndVisit('/admin/subscribers/bounces?source=ses');
    selectAllAndDelete();
    cy.wait('@del');
    cy.get('.modal.is-active').should('not.exist');
  });
});
