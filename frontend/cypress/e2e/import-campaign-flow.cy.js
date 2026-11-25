// The seams between a new list, its import and the campaign that follows (LST-63): the import
// screen makes or picks the list itself, warns (without blocking) about a list a campaign
// already went out to, and a finished import leads on to a campaign for its list. The list
// table says where each list was used. Synthetic data only; the "already used" list is the
// one reaction-lists.sql makes (a finished campaign with 2 people sent). Like import-mapping,
// this never calls resetDB.
const stamp = Date.now();
const usedList = 'Cypress reaction audience';
const usedCampaign = 'Cypress reaction recipients';
const fresh = (n) => `Flow list ${n} ${stamp}`;
const modal = '.axm-list-modal';
const submit = () => cy.get('button[type=submit]');
const pickList = (name) => {
  cy.get('.list-selector input').type(name);
  cy.contains('.list-selector .autocomplete a', name).click();
};
const chosen = () => cy.get('.list-selector .list-tags .tag.list');
const makeList = (name) => cy.request('POST', '/api/lists', {
  name, type: 'private', optin: 'single', no_team: true,
}).its('body.data');
const chooseCSV = (rows) => {
  const csv = `email,name\n${rows.map((r) => `${r}-${stamp}@example.invalid,Flow ${r}`).join('\n')}\n`;
  cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from(csv), fileName: 'flow.csv', mimeType: 'text/csv' }, { force: true });
  cy.get('[data-cy=map-panel]', { timeout: 30000 }).should('exist');
  cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
};
let usedId;
let usedCampaignId;

describe('import to campaign flow', () => {
  beforeEach(() => {
    cy.viewport(1440, 1000);
    // The address check would look the synthetic *.invalid domains up in real DNS and leave
    // their rows out (see import-mapping.cy.js).
    cy.intercept('POST', '/api/subscribers/address-check', (req) => req.reply({
      data: {
        total: req.body.emails.length, risky: 0, confirmed: 0, warning: 0, unknown: 0, pending: 0, classes: {}, issues: {},
      },
    }));
    cy.loginAndVisit('/admin/subscribers/import');
    cy.location('pathname').should('eq', '/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('form.box').should('be.visible');
    cy.request('/api/lists?minimal=true&per_page=all').its('body.data.results').then((lists) => {
      usedId = lists.find((l) => l.name === usedList).id;
    });
    cy.request('/api/campaigns?query=Cypress%20reaction%20recipients&no_body=true').its('body.data.results').then((camps) => {
      usedCampaignId = camps.find((c) => c.name === usedCampaign).id;
    });
  });

  it('says why the upload is off, and makes a new static list in the screen', () => {
    cy.get('[data-cy=import-need-list]').should('contain', 'Choose the target lists first');
    cy.get('[data-cy=import-target] label').first().should('contain', 'Target lists');
    chooseCSV(['a', 'b']);
    // A file alone is not enough: no list, no upload, and the reason stays in view.
    cy.get('[data-cy=import-need-list]').should('be.visible');
    submit().should('be.disabled');

    cy.get('[data-cy=import-new-list]').click();
    cy.get(modal).should('be.visible');
    // A dynamic list is no import target: the form hides the composition choice.
    cy.get('[data-cy=list-composition]').should('not.exist');
    cy.get(`${modal} input[name=name]`).type(fresh('new'));
    cy.get('[data-cy=btn-save]').click();
    cy.get(modal).should('not.exist');
    chosen().should('have.length', 1).and('contain', fresh('new'));
    cy.get('[data-cy=import-need-list]').should('not.exist');
    submit().should('not.be.disabled');
    cy.request('/api/lists?query=' + encodeURIComponent(fresh('new'))).its('body.data.results.0').then((list) => {
      expect(list.type).to.eq('private');
      expect(list.axm_filter || null).to.eq(null);
    });

    // Another new list goes next to the chosen one.
    cy.get('[data-cy=import-new-list]').click();
    cy.get(`${modal} input[name=name]`).type(fresh('second'));
    cy.get('[data-cy=btn-save]').click();
    chosen().should('have.length', 2);
  });

  it('warns about a list a campaign already went out to, and does not block', () => {
    const empty = fresh('empty');
    makeList(empty);
    cy.reload();
    cy.intercept('GET', '/api/lists/campaigns*').as('used');
    chooseCSV(['c']);
    pickList(empty);
    cy.wait('@used');
    cy.get('[data-cy=import-used-list]').should('not.exist');

    pickList(usedList);
    cy.wait('@used');
    cy.get('[data-cy=import-used-list]').should('be.visible')
      .and('contain', `The list "${usedList}" has already been used in a campaign`)
      .and('contain', `#${usedCampaignId}`).and('contain', usedCampaign)
      .and('contain', 'sent to 2 people').and('contain', 'get it again');
    cy.get(`[data-cy=import-used-camp-${usedCampaignId}] a`).should('have.attr', 'href').and('include', `/campaigns/${usedCampaignId}`);
    // Never blocked: no confirm window, the upload stays on.
    submit().should('not.be.disabled');

    // Leaving the used list takes the warning away.
    cy.get(`.list-selector .tag[data-id="${usedId}"] .delete`).click();
    cy.get('[data-cy=import-used-list]').should('not.exist');
  });

  it('swaps the used list for a new one from the warning', () => {
    const empty = fresh('keep');
    makeList(empty);
    cy.reload();
    chooseCSV(['d']);
    pickList(empty);
    pickList(usedList);
    cy.get('[data-cy=import-used-list]').should('be.visible');
    cy.get('[data-cy=import-used-new-list]').click();
    cy.get(`${modal} input[name=name]`).type(fresh('swap'));
    cy.get('[data-cy=btn-save]').click();
    cy.get(modal).should('not.exist');
    chosen().should('have.length', 2).and('contain', empty).and('contain', fresh('swap'))
      .and('not.contain', usedList);
    cy.get('[data-cy=import-used-list]').should('not.exist');
  });

  it('leads on to a campaign for the list a finished import filled', () => {
    const list = fresh('go');
    makeList(list).then(({ id }) => {
      cy.reload();
      chooseCSV(['e', 'f']);
      pickList(list);
      submit().click();
      cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
      cy.get('[data-cy=import-done-into]').should('contain', list).and('contain', '2');
      cy.get('[data-cy=import-used-list]').should('not.exist');
      cy.get('[data-cy=import-done]').should('be.visible').and('not.have.class', 'is-primary');
      cy.get('[data-cy=import-make-campaign]').should('have.class', 'is-primary').click();
      cy.location('pathname').should('eq', '/admin/campaigns/new');
      cy.location('search').should('contain', `list_id=${id}`);
      cy.get('.list-selector .tag').should('contain', list);
      // The finished state was cleared: the import screen is the input form again.
      cy.visit('/admin/subscribers/import');
      cy.get('form.box').should('be.visible');
      cy.request('/api/import/subscribers').its('body.data.status').should('eq', 'none');
    });
  });

  it('warns again on the finished screen when the list is already used (here: a scheduled campaign)', () => {
    const list = fresh('sched');
    const when = new Date(Date.now() + 30 * 24 * 3600 * 1000).toISOString();
    makeList(list).then(({ id }) => {
      cy.request('POST', '/api/campaigns', {
        name: `Flow scheduled ${stamp}`, subject: 'Flow subject', type: 'regular', messenger: 'email', content_type: 'richtext', body: '<p>Hi</p>', lists: [id], send_at: when,
      }).then(({ body }) => {
        cy.request('PUT', `/api/campaigns/${body.data.id}/status`, { status: 'scheduled' });
        cy.reload();
        chooseCSV(['g']);
        pickList(list);
        cy.get('[data-cy=import-used-list]').should('contain', `#${body.data.id}`)
          .and('contain', 'scheduled').and('contain', 'may receive it when it starts');
        submit().click();
        cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
        cy.get('[data-cy=import-used-list]').should('contain', `#${body.data.id}`);
        // Not blocked: the button is still there.
        cy.get('[data-cy=import-make-campaign]').should('be.visible');
        cy.get('[data-cy=import-done]').click();
        cy.get('form.box').should('be.visible');
      });
    });
  });

  it('has no campaign button after a blocklist import', () => {
    cy.get('[data-cy=check-blocklist]').click();
    cy.get('[data-cy=import-target]').should('not.exist');
    chooseCSV(['h']);
    submit().click();
    cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
    cy.get('[data-cy=import-make-campaign]').should('not.exist');
    cy.get('[data-cy=import-done]').should('have.class', 'is-primary').click();
    cy.get('form.box').should('be.visible');
  });

  it('clears only the import it was asked about', () => {
    const list = fresh('stale');
    makeList(list).then(({ id }) => {
      cy.reload();
      chooseCSV(['i']);
      pickList(list);
      submit().click();
      cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
      cy.request('/api/import/subscribers').its('body.data.axm_import_id').then((importId) => {
        // An older import's id leaves the finished one alone; its own id clears it.
        cy.request('DELETE', `/api/import/subscribers?axm_import_id=${importId + 1}`).its('body.data.status').should('eq', 'finished');
        cy.request('/api/import/subscribers').its('body.data.status').should('eq', 'finished');
        cy.request('DELETE', `/api/import/subscribers?axm_import_id=${importId}`);
        cy.request('/api/import/subscribers').its('body.data.status').should('eq', 'none');
      });
    });
  });

  it('shows where a list was used in the list table', () => {
    const empty = fresh('table');
    makeList(empty);
    cy.visit('/admin/lists');
    cy.get('input[name=query]').clear().type(`${usedList}{enter}`);
    cy.contains('tbody tr', usedList).find('[data-cy=list-campaigns]')
      .should('contain', `#${usedCampaignId}`).and('contain', usedCampaign).and('contain', '2');
    cy.contains('tbody tr', usedList).find('[data-cy=list-campaign-link]').should('have.attr', 'href').and('include', `/campaigns/${usedCampaignId}`);
    cy.get('input[name=query]').clear().type(`${empty}{enter}`);
    cy.contains('tbody tr', empty).find('[data-cy=list-campaigns]').should('contain', '—').and('not.contain', '#');
  });

  it('answers the campaigns of a list over the API (rights and names: the Go tests)', () => {
    // The API: the number and the count come with the list; a bad id is a 400.
    cy.request(`/api/lists/campaigns?list_id=${usedId}`).its('body.data').then((rows) => {
      expect(rows).to.have.length(1);
      expect(rows[0].campaign_id).to.eq(usedCampaignId);
      expect(rows[0].sent).to.eq(2);
      expect(rows[0].name).to.eq(usedCampaign);
    });
    cy.request({ url: '/api/lists/campaigns?list_id=abc', failOnStatusCode: false }).its('status').should('eq', 400);
  });
});
