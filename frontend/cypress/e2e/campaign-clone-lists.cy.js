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

const person = (n, lists) => cy.request('POST', '/api/subscribers', {
  email: `clone-${n}-${stamp}@example.invalid`, name: `Clone ${n}`, status: 'enabled', lists, preconfirm_subscriptions: true,
});

const openWindow = () => {
  cy.loginAndVisit('/admin/campaigns');
  cy.get('input[name=query]').clear().type(`${origName}{enter}`);
  cy.get('tbody tr').should('have.length', 1);
  cy.get('[data-cy=btn-clone]').click();
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

  it('fills the original\'s lists on one click; a campaign that never sent says nobody received it, and what the count covers', () => {
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
    cy.get('[data-cy=clone-received][data-state=none]')
      .should('contain', 'Nobody on the chosen lists received the original in the last 30 days')
      .find('[data-cy=clone-received-scope]')
      .should('contain', `Only the send records of the original "${origName}" from the last 30 days are counted`);
    cy.get('[data-cy=clone-received][data-state=warn]').should('not.exist');
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
    // The count says what it covers: this one campaign, the last 30 days.
    cy.get('[data-cy=clone-received-scope]')
      .should('contain', `Only the send records of the original "${origName}" from the last 30 days are counted`)
      .and('contain', 'another campaign');
    // The way out is the list made from the exclusion result, not the rule alone.
    cy.get('[data-cy=clone-received-way]').should('contain', 'Make a new list')
      .and('contain', 'Turning the rule on alone does not remove them');
    cy.get('[data-cy=btn-clone-confirm]').should('contain', 'Clone as it is').and('not.be.disabled').click();
    cy.location('pathname').should('match', /\/admin\/campaigns\/\d+$/);
    cloneLists(name).then((lists) => {
      expect(lists.map((l) => l.id)).to.have.members([listA, listB]);
    });
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
