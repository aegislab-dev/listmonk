const campaign = 'Cypress reaction recipients';
const team = 'Cypress reaction team';
const modal = '[data-cy=reaction-list]';
const input = (axis, condition) => `[data-cy=reaction-${axis}-${condition}] input`;
let campaignId;

function onCampaign() {
  cy.visit('/admin/campaigns');
  cy.contains('tr', campaign).find('[data-cy=btn-campaign-reaction]').click();
  cy.get(modal).should('be.visible');
}

function patchPreview(change) {
  cy.intercept('POST', '**/reaction-list/preview', (request) => {
    request.continue((response) => change(response.body.data, request.body));
  });
}

describe('campaign reaction lists', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request({ url: '/api/campaigns', qs: { query: campaign } }).then(({ body }) => {
      const row = body.data.results.find((item) => item.name === campaign);
      expect(row, 'synthetic campaign').to.exist;
      campaignId = row.id;
    });
  });

  beforeEach(() => cy.viewport(1280, 1000));

  it('checks a combination and saves a real private list', () => {
    cy.intercept('POST', '**/reaction-list').as('makeReaction');
    onCampaign();
    cy.get('[data-cy=reaction-create]').should('be.disabled');
    cy.get(input('open', 'yes')).check({ force: true });
    cy.get(input('click', 'no')).check({ force: true });
    cy.get('[data-cy=reaction-check]').click();
    cy.get('[data-cy=reaction-count-eligible]').should('have.text', '1');
    cy.get('[data-cy=reaction-sample]').should('contain', 'al***@e***.invalid').and('not.contain', 'alice-reaction');
    cy.get(input('click', 'any')).check({ force: true });
    cy.get('[data-cy=reaction-preview]').should('not.exist');
    cy.get('[data-cy=reaction-create]').should('be.disabled');
    cy.get(input('click', 'no')).check({ force: true });
    cy.get('[data-cy=reaction-check]').click();
    cy.get('[data-cy=reaction-count-eligible]').should('have.text', '1');
    cy.get('[data-cy=reaction-team] select, select[data-cy=reaction-team]').select('__none__');
    cy.get('[data-cy=reaction-name] input, input[data-cy=reaction-name]').invoke('val').should('contain', campaign);
    cy.get('[data-cy=reaction-create]').click();
    cy.wait('@makeReaction').then(({ request, response }) => {
      expect(request.body).to.include({ open: 'yes', click: 'no', no_team: true });
      expect(response.statusCode).to.eq(200);
      const { list } = response.body.data;
      expect(list).to.include({ type: 'private', optin: 'single', status: 'active', subscriber_count: 1 });
      expect(list.tags).to.deep.equal(['반응-리스트']);
      cy.get('[data-cy=reaction-done-count]').should('contain', '1');
      cy.get('[data-cy=reaction-campaign]').should('be.visible');
      cy.get('[data-cy=reaction-view]').click();
      cy.location('pathname').should('eq', `/admin/lists/${list.id}`);
    });
  });

  it('opens from analysis and locks the open filter when there are no individual opens', () => {
    patchPreview((data) => { data.tracking.viewers = 0; });
    cy.visit(`/admin/campaigns/analytics?id=${campaignId}`);
    cy.get('[data-cy=btn-analytics-reaction]').click();
    cy.get('[data-cy=reaction-no-open]').should('be.visible');
    cy.get(input('open', 'yes')).should('be.disabled');
    cy.get(input('open', 'no')).should('be.disabled');
    cy.get(input('open', 'any')).should('be.checked');
    cy.get(input('click', 'no')).should('not.be.disabled');
  });

  it('locks checking and creation without send records', () => {
    patchPreview((data) => { data.tracking.sends = 0; });
    onCampaign();
    cy.get('[data-cy=reaction-no-sends]').should('be.visible');
    cy.get('[data-cy=reaction-check]').should('be.disabled');
    cy.get('[data-cy=reaction-create]').should('be.disabled');
  });

  it('shows exclusions and prevents creation with zero eligible people', () => {
    patchPreview((data, body) => {
      if (body.tracking_only) return;
      data.counts = { matched: 2, blocklisted: 2, campaign_opt_out: 0, all_opt_out: 0, out_of_scope: 0, eligible: 0 };
      data.sample = [];
    });
    onCampaign();
    cy.get('[data-cy=reaction-check]').click();
    cy.get('[data-cy=reaction-count-blocked]').should('have.text', '2');
    cy.get('[data-cy=reaction-empty]').should('be.visible');
    cy.get('[data-cy=reaction-create]').should('be.disabled');
  });

  it('displays the fixed team to a team member', () => {
    // Profile fixture exercises the member UI. DB tests exercise the real permission scope.
    cy.intercept('GET', '/api/profile', (request) => {
      request.continue((response) => {
        const { data } = response.body;
        data.user_role = { id: 9, permissions: ['campaigns:get_all', 'campaigns:get_analytics'] };
        data.list_role = { team_tag: `팀:${team}`, lists: [] };
      });
    });
    onCampaign();
    cy.get('[data-cy=reaction-check]').click();
    cy.get('[data-cy=reaction-team-fixed]').should('contain', team);
    cy.get('[data-cy=reaction-team]').should('not.exist');
  });
});
