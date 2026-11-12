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

  it('compares numbers, reads number-like text, and reports values that are not numbers', () => {
    const amounts = [150000, '100,000', ' 1e5 ', '99,999', '100,000원', ''];
    cy.wrap(amounts).each((amount, i) => cy.request('POST', '/api/subscribers', {
      email: `dynamic-${baseId}-amount-${i}@example.invalid`, name: `가상 금액 ${i}`, status: 'enabled',
      attribs: { amount }, lists: [baseId],
    }));
    // Matches the number condition but is blocklisted, so it is reported as left out.
    cy.request('POST', '/api/subscribers', {
      email: `dynamic-${baseId}-blocked@example.invalid`, name: '가상 차단', status: 'blocklisted',
      attribs: { amount: 200000 }, lists: [baseId],
    });
    const name = `Dynamic numbers ${Date.now()}`;
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('input[name=name]').type(name);
    cy.get('[data-cy=dynamic-base]').select('lists');
    cy.get('.dynamic-filter .list-selector input').type(baseName);
    cy.contains('.dynamic-filter .list-selector .autocomplete a', baseName).click();
    cy.get('[data-cy=dynamic-key-0]').type('amount');

    // A number input shows thousands separators after leaving it and explains the rule.
    cy.get('[data-cy=dynamic-op-0]').select('gte');
    cy.get('[data-cy=dynamic-number-help]').should('be.visible');
    cy.get('[data-cy=dynamic-value-0]').should('have.attr', 'inputmode', 'decimal').type('100000').blur()
      .should('have.value', '100,000');
    // The rule is spelled out as a sentence before anything is counted.
    cy.get('[data-cy=dynamic-summary]').should('contain', baseName).and('contain', 'amount is at least 100,000');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', '3');
    cy.get('[data-cy=dynamic-not-number]').should('contain', '1');
    // The count comes with the members themselves, the item the condition reads, and who was left out.
    cy.get('[data-cy=dynamic-excluded]').should('contain', '1 blocklisted');
    cy.get('[data-cy=dynamic-sample] tbody tr').should('have.length', 3);
    cy.get('[data-cy=dynamic-sample] thead').should('contain', 'amount');
    cy.get('[data-cy=dynamic-sample] tbody').should('contain', `dynamic-${baseId}-amount-0@example.invalid`)
      .and('contain', '150000').and('contain', '100,000').and('contain', '1e5')
      .and('not.contain', '99,999').and('not.contain', '200000');
    cy.get('[data-cy=dynamic-sample-all]').should('not.exist');

    cy.get('[data-cy=dynamic-op-0]').select('gt');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', '1');
    cy.get('[data-cy=dynamic-op-0]').select('lte');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', '3');
    cy.get('[data-cy=dynamic-op-0]').select('lt');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', '1');

    // Between takes two numbers: the first included, the second excluded.
    cy.get('[data-cy=dynamic-op-0]').select('between');
    cy.get('[data-cy=dynamic-value-0]').clear().type('99999').blur().should('have.value', '99,999');
    cy.get('[data-cy=dynamic-value2-0]').type('100000').blur().should('have.value', '100,000');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', '1');
    cy.get('[data-cy=dynamic-value2-0]').clear().type('50000').blur();
    cy.get('[data-cy=dynamic-value2-error-0]').should('be.visible');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-error]').should('be.visible');
    cy.get('[data-cy=dynamic-count]').should('not.exist');

    // Text with a unit is rejected at the input, and blocks both the count and saving.
    cy.get('[data-cy=dynamic-op-0]').select('gte');
    cy.get('[data-cy=dynamic-value2-0]').should('not.exist');
    cy.get('[data-cy=dynamic-value-0]').clear().type('100,000원').blur();
    cy.get('[data-cy=dynamic-value-error-0]').should('be.visible');
    submit().click();
    cy.get('.dynamic-list-form').should('be.visible');
    cy.get('[data-cy=dynamic-value-0]').clear().type('100,000').blur();
    cy.get('[data-cy=dynamic-value-error-0]').should('not.exist');
    // The text condition still matches the unit value that number conditions skip.
    cy.get('[data-cy=dynamic-add]').click();
    cy.get('[data-cy=dynamic-key-1]').type('amount');
    cy.get('[data-cy=dynamic-op-1]').select('contains');
    cy.get('[data-cy=dynamic-value-1]').type('원');
    cy.get('[data-cy=dynamic-match]').select('any');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', '4');
    cy.get('[data-cy=dynamic-remove-1]').click();
    submit().click();

    cy.get('[data-cy=query]').type(name);
    cy.get('[data-cy=btn-query]').click();
    cy.contains('tr', name).should('contain', '3');
    cy.request({ url: '/api/lists', qs: { query: name } }).then(({ body }) => {
      const list = body.data.results.find((item) => item.name === name);
      expect(list.axm_filter.conditions).to.deep.eq([{ key: 'amount', op: 'gte', value: '100000' }]);
      // The saved members are the ones the count promised.
      cy.request({ url: '/api/subscribers', qs: { list_id: list.id, per_page: 100 } }).then((res) => {
        expect(res.body.data.results.map((s) => s.attribs.amount)).to.have.members([150000, '100,000', ' 1e5 ']);
      });
      // The API stores a plain decimal whatever form was sent, and rejects text.
      cy.request('PUT', `/api/lists/${list.id}`, {
        ...list, axm_filter: { ...list.axm_filter, conditions: [{ key: 'amount', op: 'between', value: ' 5e4 ', value2: '100,000' }] },
      }).its('body.data.axm_filter.conditions').should('deep.eq', [{ key: 'amount', op: 'between', value: '50000', value2: '100000' }]);
      cy.request({
        method: 'PUT', url: `/api/lists/${list.id}`, failOnStatusCode: false,
        body: { ...list, axm_filter: { ...list.axm_filter, conditions: [{ key: 'amount', op: 'gte', value: '100,000원' }] } },
      }).its('status').should('eq', 400);
    });
    cy.reload();
    cy.get('[data-cy=query]').type(name);
    cy.get('[data-cy=btn-query]').click();
    cy.contains('tr', name).should('contain', '1');
    cy.contains('a', name).click();
    cy.get('[data-cy=dynamic-op-0]').should('have.value', 'between');
    cy.get('[data-cy=dynamic-value-0]').should('have.value', '50,000');
    cy.get('[data-cy=dynamic-value2-0]').should('have.value', '100,000');
    cy.get('[data-cy=dynamic-count]').should('contain', '1');
    cy.get('[data-cy=dynamic-summary]').should('contain', 'amount is 50,000 or more and less than 100,000');
    cy.get('[data-cy=dynamic-sample] tbody tr').should('have.length', 1).and('contain', '99,999');

    // A saved, unchanged rule links to its full member list; an edited one does not.
    cy.get('[data-cy=dynamic-sample-all]').should('have.attr', 'href').and('match', /\/subscribers\/lists\/\d+$/);
    cy.get('[data-cy=dynamic-value2-0]').clear().type('200000').blur();
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-sample] tbody tr').should('have.length', 4);
    cy.get('[data-cy=dynamic-sample-all]').should('not.exist');

    cy.viewport(390, 844);
    cy.get('[data-cy=dynamic-sample]').should('exist');
    cy.get('[data-cy=dynamic-value2-0]').then(($input) => {
      const rect = $input[0].getBoundingClientRect();
      expect(rect.left).to.be.at.least(0);
      expect(rect.right).to.be.at.most(390);
      expect(rect.width).to.be.at.least(150);
    });
    cy.window().then((win) => {
      expect(win.document.documentElement.scrollWidth).to.eq(win.document.documentElement.clientWidth);
    });
  });

  it('shows counts but no subscribers to a user who may not read subscribers', () => {
    const username = `dynamic-limited-${baseId}`;
    cy.request('POST', '/api/roles/users', { name: username, permissions: ['lists:get_all', 'lists:manage_all'] }).then(({ body }) => {
      cy.request('POST', '/api/users', {
        username, password: 'dynamic-synthetic-only', password_login: true, email: `${username}@example.invalid`,
        name: '가상 리스트 관리자', type: 'user', status: 'enabled', user_role_id: body.data.id,
      });
    });
    cy.visit('/admin/login?next=/admin/lists');
    cy.get('input[name=username]').type(username);
    cy.get('input[name=password]').type('dynamic-synthetic-only');
    cy.get('button[type=submit]').click();
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('[data-cy=dynamic-key-0]').type('plan');
    cy.get('[data-cy=dynamic-value-0]').type('pro');
    cy.intercept('POST', '/api/lists/dynamic/preview').as('preview');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.wait('@preview').its('response.body.data').should((data) => {
      expect(data.count).to.be.at.least(1);
      expect(data.sample).to.eq(null);
    });
    cy.get('[data-cy=dynamic-count]').should('be.visible');
    cy.get('[data-cy=dynamic-sample-denied]').should('be.visible');
    cy.get('[data-cy=dynamic-sample]').should('not.exist');
    cy.loginAndVisit('/admin/lists');
    cy.location('pathname').should('eq', '/admin/lists');
  });

  it('offers the standard attributes first and points at the country code for a country name', () => {
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('[data-cy=dynamic-key-0]').focus();
    // Listed whether or not a recent subscriber has them.
    cy.get('[data-cy=dynamic-condition-0] .dropdown-item .key-option > span:first-child').should('have.length.at.least', 5).then(($items) => {
      expect([...$items].slice(0, 5).map((el) => el.textContent)).to.deep.eq(['country', 'follower', 'company', 'title', 'industry']);
    });
    cy.get('[data-cy=dynamic-condition-0] .dropdown-item').eq(1).should('contain', 'Followers').and('contain', 'Standard');
    cy.get('[data-cy=dynamic-key-0]').type('country');
    cy.get('[data-cy=dynamic-country-hint-0]').should('not.exist');
    cy.get('[data-cy=dynamic-value-0]').type('미국');
    cy.get('[data-cy=dynamic-country-hint-0]').should('contain', 'two-letter country codes');
    cy.get('[data-cy=dynamic-country-use-0]').should('contain', 'US').click();
    cy.get('[data-cy=dynamic-value-0]').should('have.value', 'US');
    cy.get('[data-cy=dynamic-country-hint-0]').should('not.exist');
    cy.get('[data-cy=dynamic-value-0]').clear();
    cy.get('[data-cy=dynamic-value-0]').type('Seoul');
    cy.get('[data-cy=dynamic-country-hint-0]').should('contain', 'e.g. US');
    cy.get('[data-cy=dynamic-country-use-0]').should('not.exist');
    // Only an exact comparison needs the code, and only the country attribute has one.
    cy.get('[data-cy=dynamic-op-0]').select('contains');
    cy.get('[data-cy=dynamic-country-hint-0]').should('not.exist');
    cy.get('[data-cy=dynamic-op-0]').select('eq');
    cy.get('[data-cy=dynamic-country-hint-0]').should('exist');
    cy.get('[data-cy=dynamic-key-0]').clear();
    cy.get('[data-cy=dynamic-key-0]').type('company');
    cy.get('[data-cy=dynamic-country-hint-0]').should('not.exist');
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
