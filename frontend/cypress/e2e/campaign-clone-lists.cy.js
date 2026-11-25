// The copy window asks for its own lists (LST-59): they start empty, the original's lists are
// one click away, and a count says how many of the chosen lists' people already got the
// original. Synthetic data only. The count comes from LST-34's preview, so a stub stands for
// "already received" (CI has no mail server to send with); the real answer is used for a
// campaign that never sent.
const stamp = Date.now();
const preview = '/api/campaigns/recent-exclude/preview';
const origName = `Clone source ${stamp}`;
const listName = (n) => `Clone list ${n} ${stamp}`;
const win = '[data-cy=clone-form]';

let listA;
let listB;
let listNew;
let origId;
let ruledId;
const ruledName = `Clone ruled ${stamp}`;

const person = (n, lists) => cy.request('POST', '/api/subscribers', {
  email: `clone-${n}-${stamp}@example.invalid`, name: `Clone ${n}`, status: 'enabled', lists, preconfirm_subscriptions: true,
});

const openWindow = (source = origName) => {
  cy.loginAndVisit('/admin/campaigns');
  cy.get('input[name=query]').clear().type(`${source}{enter}`);
  const sourceId = source === ruledName ? ruledId : origId;
  cy.get(`tbody tr a[href$="/campaigns/${sourceId}"]`).closest('tr').find('[data-cy=btn-clone]').click();
  cy.get(win).should('be.visible');
};

const pick = (name) => {
  cy.get('[data-cy=clone-lists] input').click().type(name);
  cy.contains('[data-cy=clone-lists] .autocomplete a', name).click();
};

const cloneLists = (name) => cy.request(`/api/campaigns?query=${encodeURIComponent(name)}&no_body=true`)
  .then(({ body }) => {
    const hit = body.data.results.filter((c) => c.name === name);
    expect(hit, name).to.have.length(1);
    return cy.request(`/api/campaigns/${hit[0].id}`).its('body.data.lists');
  });

const cloneId = (name) => cy.request(`/api/campaigns?query=${encodeURIComponent(name)}&no_body=true`)
  .then(({ body }) => body.data.results.filter((c) => c.name === name)[0].id);

describe('Clone window: the copy picks its own lists', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    ['A', 'B', 'New'].forEach((n) => {
      cy.request('POST', '/api/lists', {
        name: listName(n), type: 'private', optin: 'single', no_team: true,
      }).then(({ body }) => {
        if (n === 'A') listA = body.data.id;
        if (n === 'B') listB = body.data.id;
        if (n === 'New') listNew = body.data.id;
      });
    });
    cy.then(() => {
      person(1, [listA]);
      person(2, [listB]);
      person(3, [listNew]);
      cy.request('POST', '/api/campaigns', {
        name: origName,
        subject: 'Clone subject text',
        type: 'regular',
        messenger: 'email',
        content_type: 'richtext',
        body: '<p>Hello</p>',
        lists: [listA, listB],
      }).then(({ body }) => { origId = body.data.id; });
      // An original that has a recent-send rule (LST-65).
      cy.request('POST', '/api/campaigns', {
        name: ruledName, subject: 'Clone ruled', type: 'regular', messenger: 'email', content_type: 'richtext',
        body: '<p>Hello</p>', lists: [listA],
      }).then(({ body }) => {
        ruledId = body.data.id;
        cy.request('PUT', `/api/campaigns/${ruledId}/recent-exclude`, { enabled: true, days: 7, scope: 'all' });
      });
    });
  });

  it('opens with no lists, a disabled button and the original written out', () => {
    cy.intercept('POST', '/api/campaigns').as('create');
    openWindow();
    cy.get('[data-cy=clone-lists] .list-tags .tag').should('not.exist');
    cy.get('[data-cy=btn-clone-confirm]').should('be.disabled');
    cy.get('[data-cy=clone-original]').should('contain', origName)
      .and('contain', listName('A')).and('contain', listName('B'));
    cy.get('[data-cy=clone-name]').should('have.value', `Copy of ${origName}`);
    cy.get('[data-cy=clone-received]').should('not.exist');

    cy.get('[data-cy=clone-lists] label').first().should('contain', 'Lists to send to');

    // The header's close ✕ and Cancel both make nothing.
    cy.get('[data-cy=clone-close]').should('be.visible').click();
    cy.get(win).should('not.exist');
    cy.get('[data-cy=btn-clone]').click();
    cy.get(win).should('be.visible');
    cy.get('[data-cy=btn-clone-cancel]').click();
    cy.get(win).should('not.exist');
    cy.get('@create.all').should('have.length', 0);
    cy.request(`/api/campaigns?query=${encodeURIComponent(`Copy of ${origName}`)}&no_body=true`)
      .its('body.data.results').should('have.length', 0);
  });

  it('copies to the chosen list only, never the original\'s', () => {
    const name = `Clone to new ${stamp}`;
    openWindow();
    cy.get('[data-cy=clone-name]').clear().type(name);
    pick(listName('New'));
    cy.get('[data-cy=btn-clone-confirm]').should('not.be.disabled').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneLists(name).then((lists) => {
      expect(lists.map((l) => l.id)).to.deep.equal([listNew]);
    });
  });

  it('fills the original\'s lists on one click; a campaign that never sent shows no count', () => {
    const name = `Clone same ${stamp}`;
    cy.intercept('POST', preview).as('count');
    openWindow();
    cy.get('[data-cy=clone-name]').clear().type(name);
    cy.get('[data-cy=clone-same-lists]').click();
    cy.get('[data-cy=clone-lists] .list-tags .tag').should('have.length', 2);
    cy.wait('@count').then(({ request }) => {
      expect(request.body.list_ids).to.have.members([listA, listB]);
      expect(request.body.days).to.equal(30);
      expect(request.body.scope).to.equal('campaigns');
      expect(request.body.campaign_ids).to.deep.equal([origId]);
    });
    cy.get('[data-cy=clone-received]').should('not.exist');
    cy.get('[data-cy=btn-clone-confirm]').should('contain', 'Clone').and('not.contain', 'as it is').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneLists(name).then((lists) => {
      expect(lists.map((l) => l.id)).to.have.members([listA, listB]);
    });
  });

  it('says how many already received the original, never blocks, and tells the way to leave them out', () => {
    const name = `Clone warned ${stamp}`;
    cy.intercept('POST', preview, {
      delay: 1500,
      body: { data: { target: 9, excluded: 7, will_send: 2, as_of: new Date().toISOString(), records_from: null } },
    }).as('count');
    openWindow();
    cy.get('[data-cy=clone-name]').clear().type(name);
    cy.get('[data-cy=clone-same-lists]').click();
    // Reading takes a while: the button is not held back.
    cy.get('[data-cy=clone-received][data-state=loading]').should('exist');
    cy.get('[data-cy=btn-clone-confirm]').should('not.be.disabled');
    cy.wait('@count');
    cy.get('[data-cy=clone-received][data-state=warn]')
      .should('contain', '7 of 9 people already received the original')
      .and('contain', origName).and('contain', '30 days');
    // The rule works now (LST-65): the window offers to leave out whoever got the original, not
    // chosen by default, and no longer says that the rule alone does not remove them.
    cy.get('[data-cy=clone-rule]').should('be.visible');
    cy.get('[data-cy=clone-rule-none]').should('be.checked');
    cy.get('[data-cy=clone-rule-inherit]').should('not.exist');
    cy.get('[data-cy=clone-received-way]').should('not.exist');
    cy.get('[data-cy=btn-clone-confirm]').should('contain', 'Clone as it is').and('not.be.disabled').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneLists(name).then((lists) => {
      expect(lists.map((l) => l.id)).to.have.members([listA, listB]);
    });
    cloneId(name).then((id) => cy.request(`/api/campaigns/${id}/recent-exclude`).its('body.data.enabled').should('eq', false));
  });

  it('leaves out whoever got the original when asked: the copy gets the rule', () => {
    const name = `Clone excluded ${stamp}`;
    cy.intercept('POST', preview, {
      body: { data: { target: 9, excluded: 7, will_send: 2, as_of: new Date().toISOString(), records_from: null } },
    }).as('count');
    openWindow();
    cy.get('[data-cy=clone-name]').clear().type(name);
    cy.get('[data-cy=clone-same-lists]').click();
    cy.wait('@count');
    cy.get('[data-cy=clone-rule-exclude]').check();
    cy.get('[data-cy=clone-received][data-state=rule]').should('contain', '7 now').and('contain', origName);
    cy.get('[data-cy=clone-rule-rest]').should('contain', '2 of 9');
    cy.get('[data-cy=btn-clone-confirm]').should('contain', 'Clone').and('not.contain', 'as it is').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneId(name).then((id) => cy.request(`/api/campaigns/${id}/recent-exclude`).its('body.data').should((rule) => {
      expect(rule).to.include({ enabled: true, days: 30, scope: 'campaigns' });
      expect(rule.campaign_ids).to.deep.equal([origId]);
    }));
  });

  it('takes over the original\'s rule by default', () => {
    const name = `Clone inherited ${stamp}`;
    openWindow(ruledName);
    cy.get('[data-cy=clone-name]').clear().type(name);
    cy.get('[data-cy=clone-same-lists]').click();
    cy.get('[data-cy=clone-rule-inherit]').should('be.checked');
    cy.get('[data-cy=btn-clone-confirm]').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneId(name).then((id) => cy.request(`/api/campaigns/${id}/recent-exclude`).its('body.data').should((rule) => {
      expect(rule).to.include({ enabled: true, days: 7, scope: 'all' });
    }));
  });

  it('copies without the rule when asked', () => {
    const name = `Clone no rule ${stamp}`;
    openWindow(ruledName);
    cy.get('[data-cy=clone-name]').clear().type(name);
    cy.get('[data-cy=clone-same-lists]').click();
    cy.get('[data-cy=clone-rule-none]').check();
    cy.get('[data-cy=btn-clone-confirm]').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneId(name).then((id) => cy.request(`/api/campaigns/${id}/recent-exclude`).its('body.data.enabled').should('eq', false));
  });

  it('requires a choice if the original rule could not be read', () => {
    const name = `Clone rule unread ${stamp}`;
    cy.intercept('GET', `/api/campaigns/${ruledId}/recent-exclude`, { statusCode: 500 }).as('ruleFailed');
    openWindow(ruledName);
    cy.wait('@ruleFailed');
    cy.get('[data-cy=clone-name]').clear().type(name);
    cy.get('[data-cy=clone-same-lists]').click();
    cy.get('[data-cy=clone-rule-error]').should('be.visible');
    cy.get('[data-cy=btn-clone-confirm]').should('be.disabled');
    cy.get('[data-cy=clone-rule-none]').check();
    cy.get('[data-cy=btn-clone-confirm]').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneId(name).then((id) => cy.request(`/api/campaigns/${id}/recent-exclude`).its('body.data.enabled').should('eq', false));
  });

  it('recovers the default inheritance on retrying the original rule read', () => {
    const url = `/api/campaigns/${ruledId}/recent-exclude`;
    cy.intercept('GET', url).as('ruleLoaded');
    cy.intercept({ method: 'GET', url, times: 1 }, { statusCode: 500 }).as('ruleFailed');
    openWindow(ruledName);
    cy.wait('@ruleFailed');
    cy.get('[data-cy=clone-same-lists]').click();
    cy.get('[data-cy=btn-clone-confirm]').should('be.disabled');
    cy.get('[data-cy=clone-rule-retry]').click();
    cy.wait('@ruleLoaded');
    cy.get('[data-cy=clone-rule-error]').should('not.exist');
    cy.get('[data-cy=clone-rule-inherit]').should('be.checked');
    cy.get('[data-cy=btn-clone-confirm]').should('not.be.disabled');
    cy.get('[data-cy=btn-clone-cancel]').click();
  });

  it('says so when the count cannot be read, and still copies', () => {
    const name = `Clone unread ${stamp}`;
    cy.intercept('POST', preview, { statusCode: 500, body: { message: 'boom' } }).as('count');
    openWindow();
    cy.get('[data-cy=clone-name]').clear().type(name);
    pick(listName('A'));
    cy.wait('@count');
    cy.get('[data-cy=clone-received][data-state=error]').should('contain', 'Could not check');
    cy.get('[data-cy=btn-clone-confirm]').should('not.be.disabled').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneLists(name).then((lists) => {
      expect(lists.map((l) => l.id)).to.deep.equal([listA]);
    });
  });
});
