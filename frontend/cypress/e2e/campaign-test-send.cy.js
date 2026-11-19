// LST-41: campaign test messages to the user's own address and to test domains, the sample
// values for addresses that are not subscribers, and the setting and audit action that go
// with them. All addresses are synthetic; the test domain is corp.example.invalid.
const stamp = Date.now();
const domain = 'corp.example.invalid';
const listName = `Test send ${stamp}`;
const subEmail = `kim-${stamp}@partner.example.invalid`;
const internal = `design-${stamp}@${domain}`;
const outside = `stranger-${stamp}@outside.example.invalid`;
const body = '<p>Hello {{ .Subscriber.Name }} of {{ .Subscriber.Attribs.company }}</p>';

let campaignId;
let subId;
let ownEmail;

const box = () => cy.get('[data-cy=test-send]');
const addEmail = (email) => box().find('input[data-cy=test-emails]').type(`${email}{enter}`);
const tag = (email) => box().find(`.taginput .tag[title="${email}"]`);
const open = () => {
  cy.visit(`/admin/campaigns/${campaignId}`);
  box().find('[data-cy=btn-test-send]').should('not.be.disabled');
};

describe('Campaign test send to addresses that are not subscribers', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('/api/profile').then(({ body: profile }) => { ownEmail = profile.data.email.toLowerCase(); });
    cy.request('PUT', '/api/settings/axm.test_domains', [domain]);
    cy.waitForBackend();
    cy.request('POST', '/api/lists', {
      name: listName, type: 'private', optin: 'single', no_team: true,
    }).then(({ body: list }) => {
      cy.request('POST', '/api/subscribers', {
        name: 'Kim Sample', email: subEmail, status: 'enabled', lists: [list.data.id], attribs: { company: 'Sky Trading' },
      }).then(({ body: sub }) => { subId = sub.data.id; });
      cy.request('POST', '/api/campaigns', {
        name: `Test send campaign ${stamp}`,
        subject: 'Hello {{ .Subscriber.Name }}',
        lists: [list.data.id],
        type: 'regular',
        content_type: 'html',
        messenger: 'email',
        body,
      }).then(({ body: campaign }) => { campaignId = campaign.data.id; });
    });
  });

  beforeEach(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.get('.menu').should('exist');
  });

  it('marks each address by how it may receive the test', () => {
    open();
    box().find('[data-cy=test-help]').should('contain', domain);
    // The account of this spec signs in with a password and can edit its own address, so
    // that address is not offered and counts like any other.
    box().find('[data-cy=test-add-own]').should('not.exist');
    addEmail(ownEmail);
    tag(ownEmail).should('have.attr', 'data-kind', 'check');
    tag(ownEmail).find('.delete').click();
    addEmail(internal.toUpperCase());
    tag(internal).should('have.attr', 'data-kind', 'internal');
    addEmail(subEmail);
    tag(subEmail).should('have.attr', 'data-kind', 'check');

    cy.intercept('POST', `/api/campaigns/${campaignId}/test`).as('testSend');
    box().find('[data-cy=btn-test-send]').click();
    cy.wait('@testSend').then(({ request, response }) => {
      expect(request.body.subscribers).to.deep.equal([internal, subEmail]);
      expect(request.body).not.to.have.property('sample');
      expect(response.statusCode).to.equal(200);
    });
    cy.get('.toast').should('contain', 'Test message sent');

    // Nobody became a subscriber.
    cy.request(`/api/subscribers?search=${encodeURIComponent(internal)}`).its('body.data.total').should('eq', 0);
  });

  it('takes at most ten addresses', () => {
    open();
    for (let i = 0; i < 10; i += 1) addEmail(`u${i}-${stamp}@${domain}`);
    box().find('.taginput .tag').should('have.length', 10);
    box().find('input[data-cy=test-emails]').should('not.exist');
    box().find('[data-cy=test-add-own]').should('not.exist');
  });

  it('refuses the whole request for an outside address and marks it', () => {
    open();
    addEmail(internal);
    addEmail(outside);
    tag(outside).should('have.attr', 'data-kind', 'check');
    cy.intercept('POST', `/api/campaigns/${campaignId}/test`).as('testSend');
    box().find('[data-cy=btn-test-send]').click();
    cy.wait('@testSend').then(({ response }) => {
      expect(response.statusCode).to.equal(400);
      expect(response.body.data.rejected).to.deep.equal([outside]);
    });
    tag(outside).should('have.attr', 'data-kind', 'rejected').and('have.class', 'is-rejected');
    tag(internal).should('have.attr', 'data-kind', 'internal');
    box().find('[data-cy=test-rejected]').should('be.visible');

    // Taking the address out clears the mark, and the rest goes out.
    tag(outside).find('.delete').click();
    box().find('[data-cy=test-rejected]').should('not.exist');
    box().find('[data-cy=btn-test-send]').click();
    cy.wait('@testSend').its('response.statusCode').should('eq', 200);
  });

  it('previews and sends with typed-in sample values', () => {
    open();
    addEmail(internal);
    box().find('[data-cy=test-sample-current]').should('contain', 'Default sample');
    box().find('[data-cy=test-sample-toggle]').click();
    box().find('[data-cy=test-sample-custom] .check').click();
    box().find('input[data-cy=test-sample-name]').type('Hong Gildong');
    // Chosen but left empty, the sample is still the typed-in one.
    box().find('[data-cy=test-sample-current]').should('contain', 'Typed in');
    // The attributes start as the standard keys; a value that is not JSON blocks the box.
    box().find('textarea[data-cy=test-sample-attribs]').should('contain.value', '"company"')
      .and('contain.value', '"country"').and('contain.value', '"follower"').clear().type('{company', { parseSpecialCharSequences: false });
    box().find('[data-cy=test-sample-error]').should('be.visible');
    box().find('[data-cy=test-sample-preview]').should('be.disabled');
    box().find('textarea[data-cy=test-sample-attribs]').clear().type('{"company": "Crevasse"}', { parseSpecialCharSequences: false });
    box().find('[data-cy=test-sample-error]').should('not.exist');
    box().find('[data-cy=test-sample-current]').should('contain', 'Hong Gildong');

    cy.intercept('POST', `/api/campaigns/${campaignId}/preview`).as('preview');
    box().find('[data-cy=test-sample-preview]').click();
    cy.wait('@preview').then(({ request, response }) => {
      expect(decodeURIComponent(request.body.replace(/\+/g, ' '))).to.contain('sample={"name":"Hong Gildong","attribs":{"company":"Crevasse"}}');
      expect(response.body).to.contain('Hello Hong Gildong of Crevasse');
    });
    cy.get('[data-cy=preview-sample]').should('contain', 'Hong Gildong');
    cy.get('.modal-card-foot button').click();

    cy.intercept('POST', `/api/campaigns/${campaignId}/test`).as('testSend');
    box().find('[data-cy=btn-test-send]').click();
    cy.wait('@testSend').then(({ request, response }) => {
      expect(request.body.sample).to.deep.equal({ name: 'Hong Gildong', attribs: { company: 'Crevasse' } });
      expect(response.statusCode).to.equal(200);
    });

    // Emptied, the values go out empty and the preview shows the same.
    box().find('input[data-cy=test-sample-name]').clear();
    box().find('textarea[data-cy=test-sample-attribs]').clear();
    box().find('[data-cy=test-sample-preview]').click();
    cy.wait('@preview').its('response.body').should('contain', 'Hello  of ');
    cy.get('.modal-card-foot button').click();
    box().find('[data-cy=btn-test-send]').click();
    cy.wait('@testSend').its('request.body.sample').should('deep.equal', { name: '', attribs: {} });
  });

  it('borrows a subscriber\'s values for the preview', () => {
    open();
    box().find('[data-cy=test-sample-toggle]').click();
    box().find('[data-cy=test-sample-subscriber] .check').click();
    box().find('[data-cy=test-sample-preview]').should('be.disabled');
    box().find('input[data-cy=test-sample-search]').type(`kim-${stamp}`);
    box().contains('.dropdown-item', subEmail).click();
    box().find('[data-cy=test-sample-picked]').should('contain', 'Kim Sample').and('contain', subEmail);

    cy.intercept('POST', `/api/campaigns/${campaignId}/preview`).as('preview');
    box().find('[data-cy=test-sample-preview]').click();
    cy.wait('@preview').then(({ request, response }) => {
      expect(decodeURIComponent(request.body)).to.contain(`sample={"subscriber_id":${subId}}`);
      expect(response.body).to.contain('Hello Kim Sample of Sky Trading');
    });
    cy.get('[data-cy=preview-sample]').should('contain', 'Kim Sample');
  });

  it('saves the test domains in the settings', () => {
    cy.visit('/admin/settings');
    cy.get('.test-send-settings .taginput .tag').should('have.length', 1).and('contain', domain);
    cy.get('.test-send-settings input[name="axm.test_domains"]').type(`other-${stamp}.example.invalid{enter}`);
    cy.intercept('PUT', '/api/settings').as('save');
    cy.get('[data-cy=btn-save]').click();
    cy.wait('@save').its('request.body').its('axm.test_domains')
      .should('deep.equal', [domain, `other-${stamp}.example.invalid`]);
    cy.waitForBackend();
    cy.request('/api/config').its('body.data.test_domains').should('deep.equal', [domain, `other-${stamp}.example.invalid`]);
    cy.request('PUT', '/api/settings/axm.test_domains', [domain]);
    cy.waitForBackend();
  });

  it('shows the test on the audit log without any address', () => {
    cy.request('/api/audit?action=campaign.test&per_page=50').then(({ body: audit }) => {
      const rows = audit.data.results.filter((r) => r.target_id === campaignId);
      expect(rows.length).to.be.at.least(3);
      expect(JSON.stringify(rows.map((r) => [r.detail, r.target_name, r.count]))).not.to.contain('@');
      const first = rows[rows.length - 1];
      expect(first.count).to.equal(2);
      expect(first.detail).to.deep.equal({
        subscribers: 1, own: 0, internal: 1, domains: [domain], sample: 'default',
      });
    });
    cy.visit('/admin/settings/audit');
    cy.contains('[data-cy=audit-action-label]', 'Test message sent').should('exist');
  });
});
