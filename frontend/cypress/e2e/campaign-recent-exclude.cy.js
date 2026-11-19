const stamp = Date.now();
const name = `Recent exclusion ${stamp}`;
const listName = `Recent recipients ${stamp}`;
const password = 'local-recent-reader-password';
const username = `recent-reader-${stamp}`;
const preview = '/api/campaigns/recent-exclude/preview';
const switchInput = '[data-cy=recent-enabled] input';
const switchControl = '[data-cy=recent-enabled] .check';

let campaignId;
let optinId;

const openCampaign = (id) => cy.visit(`/admin/campaigns/${id}`);
const rulePath = () => `/api/campaigns/${campaignId}/recent-exclude`;

describe('Recent-send exclusion settings', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', { name: listName, type: 'private', optin: 'single' }).then(({ body }) => {
      cy.request('POST', '/api/subscribers', {
        name: 'Synthetic recipient',
        email: `recent-${stamp}@example.invalid`,
        status: 'enabled',
        lists: [body.data.id],
      });
    });
    cy.request('POST', '/api/lists', { name: `Opt-in ${stamp}`, type: 'private', optin: 'double' }).then(({ body }) => {
      cy.request('POST', '/api/campaigns', {
        name: `Opt-in campaign ${stamp}`, subject: 'Opt-in', type: 'optin', lists: [body.data.id],
      }).then(({ body: campaign }) => { optinId = campaign.data.id; });
    });
    cy.request('POST', '/api/roles/users', {
      name: `Recent reader ${stamp}`, permissions: ['campaigns:get_all', 'lists:get_all', 'templates:get'],
    }).then(({ body }) => {
      cy.request('POST', '/api/users', {
        username, type: 'user', name: 'Read only', email: `read-${stamp}@example.invalid`, status: 'enabled',
        password_login: true, password, user_role_id: body.data.id,
      });
    });
  });

  it('previews a disabled-by-default rule, updates the preview and saves on create', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns').as('createCampaign');
    cy.intercept('PUT', '/api/campaigns/*/recent-exclude').as('saveRule');
    openCampaign('new');
    cy.get(switchInput).should('not.be.checked');
    cy.get('[data-cy=recent-counts]').should('not.exist');
    cy.get('input[name=name]').type(name);
    cy.get('input[name=subject]').type('Preview example');
    cy.get('.list-selector input').click().type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    cy.get(switchControl).click();

    cy.wait('@recentPreview').its('request.body').should('include', { days: 14, scope: 'all' });
    cy.get('[data-cy=recent-counts]').find('td').then(($cells) => {
      expect([...$cells].map((cell) => cell.textContent)).to.deep.equal(['1', '−0', '1']);
    });
    cy.get('[data-cy=recent-as-of]').contains(/As of \d{4}-\d{2}-\d{2} \d{2}:\d{2}/);
    cy.get('[data-cy=recent-notice]').should('contain', 'still be included when you send');

    cy.get('select[data-cy=recent-days]').select('7');
    cy.wait('@recentPreview').its('request.body.days').should('eq', 7);
    cy.get('[data-cy=recent-counts]').should('contain', 'last 7 days');
    cy.get('select[data-cy=recent-scope]').select('team');
    cy.wait('@recentPreview').its('response.statusCode').should('eq', 400);
    cy.get('[data-cy=recent-error]').should('contain', 'no team tags');
    cy.get('[data-cy=recent-counts]').should('not.exist');

    cy.get('select[data-cy=recent-scope]').select('all');
    cy.wait('@recentPreview').its('response.statusCode').should('eq', 200);
    cy.get('[data-cy=btn-continue]').click();
    cy.wait('@createCampaign').then(({ response }) => { campaignId = response.body.data.id; });
    cy.wait('@saveRule').its('response.body.data').should('include', { enabled: true, days: 7, scope: 'all' });
    cy.location('hash').should('eq', '#content');
  });

  it('loads a saved rule, warns on send and disables editing for a reader', () => {
    openCampaign(campaignId);
    cy.get(switchInput).should('be.checked').and('not.be.disabled');
    cy.get('select[data-cy=recent-days]').should('have.value', '7');
    cy.get('[data-cy=btn-start]').click();
    cy.contains('.dialog', 'The exclusion rule does not apply to sending yet.').should('be.visible');
    cy.get('.dialog button').contains('Cancel').click();

    cy.clearCookies();
    cy.visit(`/admin/login?next=/admin/campaigns/${campaignId}`);
    cy.get('input[name=username]').type(username);
    cy.get('input[name=password]').type(password);
    cy.get('button[type=submit]').click();
    cy.get(switchInput).should('be.checked').and('be.disabled');
    cy.get('select[data-cy=recent-days]').should('be.disabled');
    cy.clearCookies();
    cy.loginAndVisit(`/admin/campaigns/${campaignId}`);
  });

  it('removes a saved rule when turned off and hides it for opt-in campaigns', () => {
    cy.intercept('PUT', '/api/campaigns/*/recent-exclude').as('saveRule');
    cy.get(switchInput).should('be.checked');
    cy.get(switchControl).click();
    cy.get('[data-cy=btn-save]').click();
    cy.wait('@saveRule').its('response.body.data').should('deep.equal', { enabled: false });
    cy.request(rulePath()).its('body.data').should('deep.equal', { enabled: false });
    cy.reload();
    cy.get(switchInput).should('not.be.checked');

    cy.intercept('GET', `/api/campaigns/${optinId}`).as('loadOptin');
    openCampaign(optinId);
    cy.wait('@loadOptin').its('response.body.data.type').should('eq', 'optin');
    cy.get('[data-cy=recent-exclude]').should('not.exist');
  });
});
