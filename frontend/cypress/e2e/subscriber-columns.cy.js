// Disposable synthetic-data coverage for the attribute columns of the subscribers table:
// the list's column preset, value display, sorting, conditions, export and the narrow layout.
const run = Date.now();
const longValue = `Republic of the Philippines, National Capital Region, Metro Manila (sample ${run})`;

let displayId; // A list of five subscribers with assorted values.
let bulkId; // A list of 24 matching subscribers and two others, for "select all" actions.

const subscriber = (suffix, attribs, lists) => cy.request('POST', '/api/subscribers', {
  email: `columns-${run}-${suffix}@example.invalid`, name: `가상 ${suffix}`, status: 'enabled', attribs, lists,
});
const listSubscribers = (id, qs = {}) => cy.request({ url: '/api/subscribers', qs: { list_id: id, per_page: 100, ...qs } })
  .then(({ body }) => body.data.results);
const cells = (key) => cy.get(`tbody td.attrib-cell[data-label="${key}"]`);
const emails = () => cy.get('tbody td[data-id] > a').then(($a) => [...$a].map((a) => a.innerText.trim().split('-').pop().split('@')[0]));
const noSidewaysOverflow = () => cy.window().then((win) => {
  expect(win.document.documentElement.scrollWidth).to.be.at.most(win.innerWidth);
  expect(win.document.body.scrollWidth).to.be.at.most(win.innerWidth);
});

describe('subscriber attribute columns', () => {
  before(() => {
    cy.loginAndVisit('/admin/lists');
    cy.location('pathname').should('eq', '/admin/lists');
    cy.request('POST', '/api/lists', { name: `Columns display ${run}`, type: 'private', optin: 'single', tags: [] })
      .then(({ body }) => {
        displayId = body.data.id;
        // Before the country is filled in, subscribers only have "region".
        subscriber('a', { region: 'PH', follower: '12,500' }, [displayId]);
        subscriber('b', { region: 'PH', follower: 9000 }, [displayId]);
        subscriber('c', { region: 'SG', follower: '약 3만' }, [displayId]);
        subscriber('d', { region: 'PH' }, [displayId]);
        subscriber('e', { region: 'PH', follower: '100000', memo: 'x' }, [displayId]);
      });
    cy.request('POST', '/api/lists', { name: `Columns bulk ${run}`, type: 'private', optin: 'single', tags: [] })
      .then(({ body }) => {
        bulkId = body.data.id;
        for (let i = 1; i <= 24; i += 1) subscriber(`z${i}`, { country: 'ZZ', follower: `${i * 1000}` }, [bulkId]);
        subscriber('keep1', { country: 'PH', follower: '50000' }, [bulkId]);
        subscriber('keep2', { follower: '60000' }, [bulkId]);
      });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
  });

  it('gives every list the creator preset and offers no choice while there is only one', () => {
    cy.request('/api/config').its('body.data').should((cfg) => {
      expect(cfg.axm_column_presets).to.deep.eq([{ id: 'creator', columns: ['country', 'follower'] }]);
      expect(cfg.axm_default_column_preset).to.eq('creator');
    });
    cy.request(`/api/lists/${displayId}`).its('body.data.axm_preset').should('eq', 'creator');
    // A dynamic list gets it too, and editing a list without naming a preset keeps it.
    cy.request('POST', '/api/lists', {
      name: `Columns dynamic ${run}`,
      axm_filter: {
        version: 1, base: { type: 'lists', lists: [displayId] }, match: 'all', conditions: [{ key: 'region', op: 'eq', value: 'PH' }],
      },
    }).its('body.data.axm_preset').should('eq', 'creator');
    cy.request('PUT', `/api/lists/${displayId}`, { name: `Columns display ${run}`, type: 'private', optin: 'single', tags: [] })
      .its('body.data.axm_preset').should('eq', 'creator');
    cy.request({
      method: 'PUT', url: `/api/lists/${displayId}`, failOnStatusCode: false,
      body: { name: `Columns display ${run}`, type: 'private', optin: 'single', tags: [], axm_preset: 'nope' },
    }).its('status').should('eq', 400);
    cy.request({
      method: 'POST', url: '/api/lists', failOnStatusCode: false, body: { name: `Columns bad ${run}`, axm_preset: 'nope' },
    }).its('status').should('eq', 400);

    cy.visit(`/admin/lists/${displayId}`);
    cy.get('[data-cy=btn-save]').should('be.visible');
    cy.get('[data-cy=list-preset]').should('not.exist');
    cy.visit('/admin/lists');
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('[data-cy=list-preset]').should('not.exist');
  });

  it('shows the preset columns with a dash for missing values, before and after country is filled in', () => {
    cy.visit(`/admin/subscribers/lists/${displayId}`);
    cy.get('[data-cy=count]').should('have.text', '5');
    cy.get('thead th.attrib-head').then(($th) => [...$th].map((th) => th.innerText.trim()))
      .should('deep.eq', ['country', 'follower']);
    cy.get('[data-cy=column-preset]').should('not.exist');
    cy.get('[data-cy=btn-columns]').should('not.exist');
    // Nobody has a country yet: the column is there and every cell is a dash.
    cells('country').should('have.length', 5).each(($td) => {
      expect($td.find('.attrib-empty')).to.have.length(1);
      expect($td.text().trim()).to.eq('—');
    });
    // Newest first: e, d, c, b, a. Values show as stored.
    cells('follower').then(($td) => [...$td].map((td) => td.innerText.trim()))
      .should('deep.eq', ['100000', '—', '약 3만', '9000', '12,500']);
    noSidewaysOverflow();
    // Sorting and filtering on the empty column work and change nothing.
    cy.get('th.attrib-head').contains('country').click();
    cy.get('[data-cy=count]').should('have.text', '5');
    cells('country').should('have.length', 5);

    // The country gets filled in: text, a long value, a list value and a nested value.
    listSubscribers(displayId).then((subs) => {
      const values = {
        a: 'PH', b: longValue, c: ['SG', 'MY'], d: { code: 'PH', city: 'Cebu' },
      };
      subs.forEach((s) => {
        const key = s.email.split('@')[0].split('-').pop();
        if (!(key in values)) return;
        cy.request('PUT', `/api/subscribers/${s.id}`, {
          email: s.email, name: s.name, status: 'enabled', lists: [displayId], attribs: { ...s.attribs, country: values[key] },
        });
      });
    });
    cy.visit(`/admin/subscribers/lists/${displayId}`);
    cells('country').should('have.length', 5);
    cy.contains('tbody tr', `columns-${run}-a@`).find('td[data-label=country]').should('have.text', 'PH');
    cy.contains('tbody tr', `columns-${run}-c@`).find('td[data-label=country]').should('contain.text', 'SG, MY');
    cy.contains('tbody tr', `columns-${run}-d@`).find('td[data-label=country] .attrib-value')
      .should('have.text', '{"city":"Cebu","code":"PH"}');
    cy.contains('tbody tr', `columns-${run}-e@`).find('td[data-label=country] .attrib-empty').should('exist');
    // A long value is cut to one line and opens in place.
    cy.contains('tbody tr', `columns-${run}-b@`).find('td[data-label=country]').within(() => {
      cy.get('.attrib-value').should('have.attr', 'title', longValue).and('not.have.class', 'is-open');
      cy.get('.attrib-value').then(($el) => expect($el[0].scrollWidth).to.be.greaterThan($el[0].clientWidth));
      cy.get('.attrib-more').should('have.attr', 'aria-expanded', 'false').click();
      cy.get('.attrib-value').should('have.class', 'is-open');
      cy.get('.attrib-value').then(($el) => expect($el[0].scrollWidth).to.be.at.most($el[0].clientWidth));
      cy.get('.attrib-more').should('have.attr', 'aria-expanded', 'true').click();
      cy.get('.attrib-value').should('not.have.class', 'is-open');
    });
    noSidewaysOverflow();
  });

  it('sorts by an attribute column: numbers by value, then text, missing values last', () => {
    cy.visit(`/admin/subscribers/lists/${displayId}`);
    cells('follower').should('have.length', 5);
    cy.intercept('GET', '/api/subscribers*').as('subs');
    cy.get('th.attrib-head .attrib-head-label').contains('follower').click();
    cy.wait('@subs').its('request.url').should('include', 'order_by=attribs.follower');
    cy.get('@subs').its('request.url').then((url) => {
      const order = new URL(url, 'http://x').searchParams.get('order');
      const want = order === 'asc' ? ['b', 'a', 'e', 'c', 'd'] : ['e', 'a', 'b', 'c', 'd'];
      emails().should('deep.eq', want);
    });
    cy.get('th.attrib-head .attrib-head-label').contains('follower').click();
    cy.wait('@subs').its('request.url').then((url) => {
      const order = new URL(url, 'http://x').searchParams.get('order');
      const want = order === 'asc' ? ['b', 'a', 'e', 'c', 'd'] : ['e', 'a', 'b', 'c', 'd'];
      emails().should('deep.eq', want);
    });
  });

  it('filters by conditions from the column header and the toolbar, and shows them as chips', () => {
    cy.visit(`/admin/subscribers/lists/${bulkId}`);
    cy.get('[data-cy=count]').should('have.text', '26');
    cy.get('[data-cy=attrib-filters]').should('not.exist');

    // From the header: the key is filled in and the value has the focus.
    cy.get('[data-cy=attrib-head-filter-country]').click();
    cy.get('[data-cy=attrib-filter-key]').should('have.value', 'country');
    cy.focused().should('have.attr', 'data-cy', 'attrib-filter-value');
    cy.get('[data-cy=attrib-filter-apply]').click();
    cy.get('[data-cy=attrib-filter-form] .help.is-danger').should('be.visible');
    cy.get('[data-cy=count]').should('have.text', '26');
    cy.get('[data-cy=attrib-filter-value]').type('ZZ');
    cy.get('[data-cy=attrib-filter-apply]').click();
    cy.get('[data-cy=attrib-filter-form]').should('not.exist');
    cy.get('[data-cy=attrib-chip-0]').should('contain.text', 'country').and('contain.text', 'ZZ');
    cy.get('[data-cy=count]').should('have.text', '24');
    cy.get('[data-cy=attrib-head-filter-country]').should('have.class', 'is-active');

    // From the toolbar: a number condition refuses text and reads thousands commas.
    cy.get('[data-cy=btn-attrib-filter]').click();
    cy.get('[data-cy=attrib-filter-key]').type('follower');
    cy.get('[data-cy=attrib-filter-op]').select('gte');
    cy.get('[data-cy=attrib-filter-value]').type('20,000원');
    cy.get('[data-cy=attrib-filter-apply]').click();
    cy.get('[data-cy=attrib-filter-form] .help.is-danger').should('be.visible');
    cy.get('[data-cy=attrib-chip-1]').should('not.exist');
    cy.get('[data-cy=attrib-filter-value]').clear().type('20,000');
    cy.get('[data-cy=attrib-filter-apply]').click();
    cy.get('[data-cy=attrib-chip-1]').should('contain.text', 'follower').and('contain.text', '20,000');
    cy.get('[data-cy=count]').should('have.text', '5');
    cells('follower').then(($td) => [...$td].map((td) => Number(td.innerText.trim())).sort((a, b) => a - b))
      .should('deep.eq', [20000, 21000, 22000, 23000, 24000]);

    // The export uses the same conditions and adds the preset columns.
    cy.get('[data-cy=btn-export-subscribers]').click();
    cy.get('.modal .media-content').should('contain.text', 'country, follower');
    cy.get('.modal button:not(.is-primary)').click();
    cy.request({
      url: '/api/subscribers/export',
      qs: {
        list_id: bulkId,
        attrib_filter: JSON.stringify([{ key: 'country', op: 'eq', value: 'ZZ' }, { key: 'follower', op: 'gte', value: '20000' }]),
      },
    }).its('body').should((csv) => {
      const lines = csv.trim().split('\n');
      expect(lines[0]).to.eq('uuid,email,name,attributes,status,created_at,updated_at');
      expect(lines).to.have.length(6);
    });
    cy.request(`/api/subscribers/export?list_id=${bulkId}&attrib_col=country&attrib_col=follower&attrib_col=name`)
      .its('body').should((csv) => {
        const lines = csv.trim().split('\n');
        expect(lines[0]).to.eq('uuid,email,name,attributes,status,created_at,updated_at,country,follower,attributes.name');
        expect(lines).to.have.length(27);
        expect(lines.find((l) => l.includes(`columns-${run}-z24@`))).to.match(/,ZZ,24000,$/);
        expect(lines.find((l) => l.includes(`columns-${run}-keep2@`))).to.match(/,,60000,$/);
      });

    // Removing chips widens the list again.
    cy.get('[data-cy=attrib-chip-1] .delete').click();
    cy.get('[data-cy=count]').should('have.text', '24');
    cy.get('[data-cy=attrib-filter-clear]').should('not.exist');
    cy.get('[data-cy=attrib-chip-0] .delete').click();
    cy.get('[data-cy=count]').should('have.text', '26');
    cy.get('[data-cy=attrib-filters]').should('not.exist');
  });

  it('rejects malformed conditions and keeps keys and values as data', () => {
    const bad = (filter) => cy.request({
      url: '/api/subscribers', qs: { list_id: bulkId, attrib_filter: filter }, failOnStatusCode: false,
    }).its('status').should('eq', 400);
    bad('{}');
    bad(JSON.stringify([{ key: 'country', op: 'drop' }]));
    bad(JSON.stringify([{ key: 'follower', op: 'gte', value: 'abc' }]));
    bad(JSON.stringify([{ key: 'country', op: 'empty', sql: '1=1' }]));
    cy.request({
      url: '/api/subscribers',
      qs: {
        list_id: bulkId, per_page: 100, order_by: "attribs.x'); DROP TABLE lists; --",
        attrib_filter: JSON.stringify([{ key: '%order%', op: 'eq', value: "%query%' OR 1=1 --" }]),
      },
    }).its('body.data.total').should('eq', 0);
    cy.request(`/api/lists/${bulkId}`).its('status').should('eq', 200);
  });

  it('applies the conditions to "select all" actions, never touching subscribers outside them', () => {
    cy.visit(`/admin/subscribers/lists/${bulkId}`);
    cy.get('[data-cy=count]').should('have.text', '26');
    const addCondition = (key, op, value) => {
      cy.get('[data-cy=btn-attrib-filter]').click();
      cy.get('[data-cy=attrib-filter-key]').type(key);
      cy.get('[data-cy=attrib-filter-op]').select(op);
      cy.get('[data-cy=attrib-filter-value]').type(value);
      cy.get('[data-cy=attrib-filter-apply]').click();
    };
    const selectAll = (num) => {
      cy.get('thead th.checkbox-cell .checkbox').click();
      cy.get('.b-table .actions').contains('a', `${num}`).click();
    };

    // Blocklist everyone with country ZZ and 3,000 followers or more: 22 of 26.
    addCondition('country', 'eq', 'ZZ');
    addCondition('follower', 'gte', '3000');
    cy.get('[data-cy=count]').should('have.text', '22');
    selectAll(22);
    cy.intercept('PUT', '/api/subscribers/query/blocklist').as('blocklist');
    cy.get('[data-cy=btn-manage-blocklist]').click();
    cy.get('.modal button.is-primary').click();
    cy.wait('@blocklist').then(({ request, response }) => {
      expect(response.statusCode).to.eq(200);
      expect(request.body.all).to.eq(false);
      expect(request.body.attrib_filter).to.have.length(2);
    });
    listSubscribers(bulkId).should((subs) => {
      expect(subs).to.have.length(26);
      const blocked = subs.filter((s) => s.status === 'blocklisted');
      expect(blocked).to.have.length(22);
      blocked.forEach((s) => {
        expect(s.attribs.country).to.eq('ZZ');
        expect(Number(s.attribs.follower)).to.be.at.least(3000);
      });
    });

    // Delete everyone with country ZZ: 24 of 26. The two others stay.
    cy.visit(`/admin/subscribers/lists/${bulkId}`);
    addCondition('country', 'eq', 'ZZ');
    cy.get('[data-cy=count]').should('have.text', '24');
    selectAll(24);
    cy.intercept('POST', '/api/subscribers/query/delete').as('delete');
    cy.get('[data-cy=btn-delete-subscribers]').click();
    cy.get('.modal button.is-primary').click();
    cy.wait('@delete').then(({ request, response }) => {
      expect(response.statusCode).to.eq(200);
      expect(request.body.all).to.eq(false);
      expect(request.body.attrib_filter).to.deep.eq([{ key: 'country', op: 'eq', value: 'ZZ' }]);
    });
    listSubscribers(bulkId).should((subs) => {
      expect(subs.map((s) => s.email.split('@')[0].split('-').pop()).sort()).to.deep.eq(['keep1', 'keep2']);
    });
    // The other list was never in scope.
    listSubscribers(displayId).should('have.length', 5);
  });

  it('fits a 390px screen: one card per subscriber, no sideways overflow', () => {
    cy.viewport(390, 844);
    cy.visit(`/admin/subscribers/lists/${displayId}`);
    cells('country').should('have.length', 5);
    noSidewaysOverflow();
    cy.get('thead th.attrib-head').should('not.be.visible');
    cy.contains('tbody tr', `columns-${run}-a@`).within(() => {
      cy.get('td[data-label=country]').should('be.visible').and('contain.text', 'PH');
      cy.get('td[data-label=follower]').should('be.visible').and('contain.text', '12,500');
      cy.get('td.sub-email-cell a').first().then(($a) => {
        const box = $a[0].getBoundingClientRect();
        expect(box.left).to.be.at.least(0);
        expect(box.right).to.be.at.most(390);
      });
    });
    // Sorting by an attribute column is in the sort box, and conditions stack.
    cy.get('.table-mobile-sort select option').then(($o) => [...$o].map((o) => o.innerText.trim()))
      .should('include.members', ['country', 'follower']);
    cy.get('[data-cy=btn-attrib-filter]').click();
    cy.get('[data-cy=attrib-filter-key]').type('follower');
    cy.get('[data-cy=attrib-filter-op]').select('gte');
    cy.get('[data-cy=attrib-filter-value]').type('10000');
    noSidewaysOverflow();
    cy.get('[data-cy=attrib-filter-apply]').click();
    cy.get('[data-cy=count]').should('have.text', '2');
    cy.get('[data-cy=attrib-chip-0]').should('be.visible');
    noSidewaysOverflow();
    cy.contains('tbody tr', `columns-${run}-b@`).should('not.exist');
  });

  it('shows the default preset columns on the page of all subscribers', () => {
    cy.visit('/admin/subscribers');
    cy.get('thead th.attrib-head').then(($th) => [...$th].map((th) => th.innerText.trim()))
      .should('deep.eq', ['country', 'follower']);
    noSidewaysOverflow();
  });
});
