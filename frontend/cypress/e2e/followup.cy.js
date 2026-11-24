// A follow-up campaign draft made together with a reaction list (LST-47). It runs on its own
// campaign of reaction-lists.sql, so that the follow-up does not change what reaction-lists.cy.js
// finds. A campaign has one follow-up, so the steps run in order.
const campaign = 'Cypress followup recipients';
const modal = '[data-cy=reaction-list]';
const teamSelect = '[data-cy=reaction-team] select, select[data-cy=reaction-team]';
const input = (axis, condition) => `[data-cy=reaction-${axis}-${condition}] input`;
const toggle = '[data-cy=followup-toggle] input';
let campaignId;
let followupId;

function openDialog() {
  cy.visit('/admin/campaigns');
  // The follow-up draft has the campaign's name in its own, so pick the finished row.
  cy.get('tbody tr').filter(`:contains("${campaign}")`).filter(':has(.tag.finished)')
    .find('[data-cy=btn-campaign-reaction]').click();
  cy.get(modal).should('be.visible');
}

// Opened and did not click: one person in the fixture.
function checkOpenedNotClicked() {
  cy.get(input('open', 'yes')).check({ force: true });
  cy.get(input('click', 'no')).check({ force: true });
  cy.get('[data-cy=reaction-check]').click();
  cy.get('[data-cy=reaction-count-eligible]').should('have.text', '1');
}

describe('follow-up campaign draft', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request({ url: '/api/campaigns', qs: { query: campaign } }).then(({ body }) => {
      campaignId = body.data.results.find((item) => item.name === campaign).id;
    });
  });

  beforeEach(() => cy.viewport(1280, 1000));

  it('opens ready to send again: the click filter preset, the box ticked once the people are checked', () => {
    openDialog();
    cy.get('#reaction-title').should('contain', "Send again to people who didn't click");
    cy.get(input('click', 'no')).should('be.checked');
    cy.get(input('open', 'any')).should('be.checked');
    cy.get('[data-cy=followup-box]').should('not.exist');
    checkOpenedNotClicked();
    cy.get('[data-cy=followup-box]').should('be.visible');
    cy.get(toggle).should('be.checked');
    // The fixture was sent just now, so the warning about the first days shows.
    cy.get('[data-cy=followup-warn]').scrollIntoView().should('be.visible').and('contain', '3 days');
    cy.get('[data-cy=followup-name] input, input[data-cy=followup-name]').invoke('val').should('contain', campaign);
    cy.get('[data-cy=followup-note]').should('contain', 'checked again when sending starts');
    cy.get('[data-cy=reaction-create]').invoke('text').should('contain', 'follow-up draft');

    // The box can still be unticked: its parts go and the list is made alone.
    cy.get(toggle).uncheck({ force: true });
    cy.get('[data-cy=followup-warn], [data-cy=followup-name], [data-cy=followup-note]').should('not.exist');
    cy.get('[data-cy=reaction-create]').invoke('text').should('not.contain', 'follow-up');
    cy.get(toggle).check({ force: true });

    // A change of condition asks for a new check, and the box goes with it.
    cy.get(input('click', 'any')).check({ force: true });
    cy.get('[data-cy=followup-box]').should('not.exist');
    cy.get(input('click', 'no')).check({ force: true });
    cy.get('[data-cy=reaction-check]').click();
    cy.get(toggle).should('be.checked');
  });

  it('shows the entry as a button with words on the finished row and on the campaign page', () => {
    cy.visit('/admin/campaigns');
    cy.get('tbody tr').filter(`:contains("${campaign}")`).filter(':has(.tag.finished)')
      .find('[data-cy=btn-campaign-reaction]').should('contain', "Send again to people who didn't click");
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.get('[data-cy=btn-campaign-followup]').should('contain', "Send again to people who didn't click").click();
    cy.get(modal).should('be.visible');
    cy.get(input('click', 'no')).should('be.checked');
    cy.get('#reaction-title').should('contain', "Send again to people who didn't click");
  });

  it('falls back to "did not open" where clicks cannot be told apart, and to neither where nothing can', () => {
    cy.intercept('POST', '**/reaction-list/preview', (request) => {
      request.continue((response) => { response.body.data.tracking.clickers = 0; });
    });
    openDialog();
    cy.get(input('click', 'any')).should('be.checked');
    cy.get(input('click', 'no')).should('be.disabled');
    cy.get(input('open', 'no')).should('be.checked');
  });

  it('does not warn after the first days', () => {
    cy.intercept('POST', '**/reaction-list/preview', (request) => {
      request.continue((response) => { response.body.data.followup.days_since = 5; });
    });
    openDialog();
    checkOpenedNotClicked();
    cy.get(toggle).check({ force: true });
    cy.get('[data-cy=followup-name]').should('exist');
    cy.get('[data-cy=followup-warn]').should('not.exist');
  });

  it('makes the list and the draft in one step and opens the draft', () => {
    cy.intercept('POST', '**/reaction-list').as('make');
    openDialog();
    checkOpenedNotClicked();
    cy.get(teamSelect).select('__none__');
    cy.get(toggle).check({ force: true });
    cy.get('[data-cy=reaction-create]').click();
    cy.wait('@make').then(({ request, response }) => {
      expect(request.body).to.include({ open: 'yes', click: 'no', followup: true, followup_name: '' });
      expect(response.statusCode).to.eq(200);
      const { followup, list } = response.body.data;
      expect(followup).to.include({ status: 'draft' });
      expect(followup.name).to.contain(campaign);
      expect(list.subscriber_count).to.eq(1);
      followupId = followup.id;
    });
    cy.get('[data-cy=reaction-done-count]').should('contain', 'follow-up draft is not scheduled');
    cy.get('[data-cy=followup-done]').should('contain', 'draft');
    cy.get('[data-cy=reaction-campaign]').should('not.exist');
    cy.get('[data-cy=followup-view]').click();
    // followupId is set by the callback above, so read it when this step runs.
    cy.then(() => cy.location('pathname').should('eq', `/admin/campaigns/${followupId}`));
  });

  it('tells the draft where it comes from, and the original where its follow-up is', () => {
    cy.visit(`/admin/campaigns/${followupId}`);
    cy.get('[data-cy=followup-banner]').should('contain', `#${campaignId}`).and('contain', campaign)
      .and('contain', 'checked again when sending starts');
    cy.get('[data-cy=followup-banner]').should('contain', '1 people').and('not.contain', 'lists of this campaign were changed');
    // Nobody has left since it was made, and the banner still says so.
    cy.get('[data-cy=followup-pending]').should('contain', '0 would be left out');
    // The note about the recent-send exclusion is under that panel, not in the banner.
    cy.get('[data-cy=followup-banner]').should('not.contain', 'recent-send exclusion');
    cy.get('[data-cy=followup-exclude-note]').should('be.visible').and('contain', 'recent-send exclusion');
    // The draft sends to the new list only.
    cy.get('.list-selector').contains('label', /\(1\)$/).should('be.visible');
    cy.get('[data-cy=followup-parent-link]').click();
    cy.location('pathname').should('eq', `/admin/campaigns/${campaignId}`);
    cy.get('[data-cy=followup-child-link]').should('contain', `#${followupId}`).find('a').click();
    cy.location('pathname').should('eq', `/admin/campaigns/${followupId}`);
  });

  it('offers the existing follow-up instead of a second one, and still makes the list alone', () => {
    cy.intercept('POST', '**/reaction-list').as('make');
    openDialog();
    checkOpenedNotClicked();
    cy.get(toggle).should('not.exist');
    cy.get('[data-cy=followup-open-existing]').should('contain', `#${followupId}`);
    // The box tells what the follow-up was made with.
    cy.get('[data-cy=followup-box]').should('contain', 'Draft').and('contain', 'Did not click').and('contain', '1 people');
    cy.get(teamSelect).select('__none__');
    cy.get('[data-cy=reaction-create]').click();
    cy.wait('@make').then(({ request, response }) => {
      expect(request.body).not.to.have.property('followup');
      expect(response.body.data).not.to.have.property('followup');
    });
    cy.get('[data-cy=reaction-done]').should('be.visible');
    cy.get('[data-cy=followup-view]').should('not.exist');
    cy.get('[data-cy=reaction-campaign]').should('be.visible');

    openDialog();
    checkOpenedNotClicked();
    cy.get('[data-cy=followup-open-existing]').click();
    cy.location('pathname').should('eq', `/admin/campaigns/${followupId}`);
  });

  it('shows the follow-up on the campaign list with its tag', () => {
    cy.visit('/admin/campaigns');
    cy.contains('tr', `${campaign} follow-up`).should('contain', 'Draft').and('contain', '후속');
  });

  it('still offers the existing follow-up when this check finds nobody', () => {
    cy.intercept('POST', '**/reaction-list/preview', (request) => {
      request.continue((response) => {
        if (request.body.tracking_only) return;
        response.body.data.counts = { matched: 0, blocklisted: 0, campaign_opt_out: 0, all_opt_out: 0, out_of_scope: 0, eligible: 0 };
        response.body.data.sample = [];
      });
    });
    openDialog();
    cy.get(input('open', 'yes')).check({ force: true });
    cy.get('[data-cy=reaction-check]').click();
    cy.get('[data-cy=reaction-empty]').should('be.visible');
    cy.get('[data-cy=followup-open-existing]').should('contain', `#${followupId}`);
    cy.get('[data-cy=reaction-create]').should('be.disabled');
  });

  it('names the condition of a follow-up made for everyone who received the campaign', () => {
    cy.intercept('POST', '**/reaction-list/preview', (request) => {
      request.continue((response) => { response.body.data.followup.existing.condition = { open: 'any', click: 'any' }; });
    });
    openDialog();
    checkOpenedNotClicked();
    cy.get('[data-cy=followup-box]').should('contain', 'Condition Received');
  });

  it('warns on the draft when its lists are changed and saved', () => {
    cy.visit(`/admin/campaigns/${followupId}`);
    cy.get('[data-cy=followup-banner]').should('be.visible');
    cy.get('[data-cy=followup-lists-changed]').should('not.exist');
    cy.get('.list-selector input').type('Cypress followup audience');
    cy.get('.list-selector').contains('.dropdown-item', 'Cypress followup audience').click();
    cy.get('[data-cy=btn-save]').click();
    // No reload: the banner reads again after the save.
    cy.get('[data-cy=followup-lists-changed]').should('be.visible');
  });

  it('keeps the box away from a campaign that is not finished', () => {
    cy.intercept('POST', '**/reaction-list/preview', (request) => {
      request.continue((response) => {
        response.body.data.followup = { available: false, reason: 'not_finished', warn_days: 3, default_name: '' };
      });
    });
    openDialog();
    checkOpenedNotClicked();
    cy.get('[data-cy=followup-box]').should('not.exist');
  });
});
