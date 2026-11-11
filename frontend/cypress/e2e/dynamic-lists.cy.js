// Disposable synthetic-data coverage for the dynamic list UI and API. No campaigns are sent.
const submit = () => cy.get('[data-cy=btn-save]');
const sample = (suffix, plan) => cy.request('POST', '/api/subscribers', {
  email: `dynamic-${suffix}@example.invalid`, name: `가상 ${suffix}`, status: 'enabled',
  attribs: { plan, tags: ['vip'] }, lists: baseId ? [baseId] : [],
});

let baseId;
let baseName;
let dynamicId;
let dynamicName;

describe('dynamic lists', () => {
  before(() => {
    cy.loginAndVisit('/admin/lists');
    baseId = null;
    dynamicId = null;
    baseName = `Dynamic source ${Date.now()}`;
    cy.request('POST', '/api/lists', { name: baseName, type: 'private', optin: 'single', tags: [] }).then(({ body }) => {
      baseId = body.data.id;
      return Promise.all([
        sample(`${baseId}-one`, 'pro'),
        sample(`${baseId}-two`, 'pro'),
        sample(`${baseId}-three`, 'free'),
      ]);
    });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
    cy.loginAndVisit('/admin/lists');
    cy.location('pathname').should('eq', '/admin/lists');
  });

  it('creates, previews, recomputes, edits, and selects a dynamic list without exposing it as an import target', () => {
    dynamicName = `Dynamic computed ${Date.now()}`;
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('input[name=name]').type(dynamicName);
    cy.get('[data-cy=dynamic-base]').select('lists');
    cy.get('.dynamic-filter .list-selector input').type(baseName);
    cy.contains('.dynamic-filter .list-selector .autocomplete a', baseName).click();
    cy.get('[data-cy=dynamic-key-0]').type('plan');
    cy.get('[data-cy=dynamic-op-0]').select('eq');
    cy.get('[data-cy=dynamic-value-0]').type('pro');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', '2');
    cy.get('[data-cy=btn-save]').click();
    cy.get('[data-cy=query]').type(dynamicName);
    cy.get('[data-cy=btn-query]').click();
    cy.contains('a', dynamicName).should('be.visible');
    cy.get('[data-cy=type-dynamic]').should('be.visible');
    cy.contains('tr', dynamicName).should('contain', '2');

    cy.request({ url: '/api/lists', qs: { query: dynamicName } }).then(({ body }) => {
      const list = body.data.results.find((item) => item.name === dynamicName);
      expect(list).to.exist;
      dynamicId = list.id;
      expect(list.type).to.eq('private');
      expect(list.optin).to.eq('single');
      expect(list.axm_filter).to.deep.include({ version: 1, match: 'all' });
    });
    cy.request('/api/public/lists').its('body').should((lists) => {
      expect(lists.map((list) => list.name)).not.to.include(dynamicName);
    });

    cy.request({ url: '/api/subscribers', qs: { query: `email = 'dynamic-${baseId}-one@example.invalid'`, per_page: 1 } })
      .then(({ body }) => {
        const subscriber = body.data.results[0];
        return cy.request('PUT', `/api/subscribers/${subscriber.id}`, {
          email: subscriber.email, name: subscriber.name, status: 'enabled', lists: [baseId],
          attribs: { ...subscriber.attribs, plan: 'free' },
        });
      });
    cy.reload();
    cy.get('[data-cy=query]').type(dynamicName);
    cy.get('[data-cy=btn-query]').click();
    cy.contains('tr', dynamicName).should('contain', '1');

    cy.contains('a', dynamicName).click();
    cy.get('[data-cy=dynamic-count]').should('contain', '1');
    cy.get('[data-cy=dynamic-key-0]').should('have.value', 'plan');
    cy.get('[data-cy=dynamic-add]').click();
    cy.get('[data-cy=dynamic-key-1]').type('tags');
    cy.get('[data-cy=dynamic-op-1]').select('has');
    cy.get('[data-cy=dynamic-value-1]').type('vip');
    cy.get('[data-cy=dynamic-match]').select('any');
    cy.get('[data-cy=dynamic-remove-1]').click();
    submit().click();

    cy.visit('/admin/campaigns/new');
    cy.get('.list-selector input').type(dynamicName);
    cy.contains('.list-selector .autocomplete a', dynamicName).click();
    cy.get('.list-selector .list-tags').should('contain', dynamicName);
    cy.get('.list-selector .list-tags small').should('contain.text', 'Dynamic');

    cy.request('DELETE', '/api/import/subscribers');
    cy.visit('/admin/subscribers/import');
    cy.get('.list-selector .list-tags').should('not.contain', dynamicName);
    cy.get('.list-selector input').type(dynamicName);
    cy.get('.list-selector .autocomplete a').should('not.exist');

    cy.visit('/admin/subscribers');
    cy.get('[data-cy=btn-new]').click();
    cy.get('.list-selector .list-tags').should('not.contain', dynamicName);
    cy.window().then((win) => {
      expect(win.document.documentElement.scrollWidth).to.eq(win.document.documentElement.clientWidth);
    });

    const ordinaryName = `Ordinary regression ${Date.now()}`;
    const renamed = `${ordinaryName} edited`;
    cy.visit('/admin/lists');
    cy.get('[data-cy=btn-new]').click();
    cy.get('input[name=name]').type(ordinaryName);
    cy.get('select[name=type]').select('public');
    cy.get('select[name=optin]').select('double');
    submit().click();
    cy.get('[data-cy=query]').type(ordinaryName);
    cy.get('[data-cy=btn-query]').click();
    cy.contains('tr', ordinaryName).find('td[data-label=Name] a').click();
    cy.get('input[name=name]').clear().type(renamed);
    cy.get('select[name=type]').should('have.value', 'public');
    cy.get('select[name=optin]').should('have.value', 'double');
    submit().click();
    cy.get('[data-cy=query]').clear().type(renamed);
    cy.get('[data-cy=btn-query]').click();
    cy.contains('tr', renamed).should('be.visible');
  });

  it('keeps the dynamic filter usable at 390px without horizontal overflow', () => {
    cy.viewport(390, 844);
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('input[name=name]').type(`Mobile dynamic ${Date.now()}`);
    cy.get('[data-cy=dynamic-add]').focus().type('{enter}');
    cy.get('[data-cy=dynamic-key-1]').should('be.focused');
    cy.get('[data-cy=dynamic-remove-1]').focus().type('{enter}');
    cy.get('[data-cy=dynamic-condition-1]').should('not.exist');
    cy.get('[data-cy=dynamic-add]').should('be.focused');
    cy.window().then((win) => {
      expect(win.document.documentElement.scrollWidth).to.eq(win.document.documentElement.clientWidth);
    });
    cy.get('.modal-card-foot').then(($footer) => {
      const rect = $footer[0].getBoundingClientRect();
      expect(rect.right).to.be.at.most(390);
      expect(rect.left).to.be.at.least(0);
    });
  });
});
