// Disposable synthetic-data coverage for drawing a list into a new list, and for the exclude
// lists of a dynamic list (LST-23). No campaign is sent.
const stamp = Date.now();
const srcName = `Draw source ${stamp}`;
const exName = `Draw exclude ${stamp}`;
let srcId;
let exId;

const people = 30;
const excluded = 5;

const openList = (name) => {
  cy.get('[data-cy=query]').clear().type(name);
  cy.get('[data-cy=btn-query]').click();
  return cy.contains('tr', name);
};

describe('list draws and exclude lists', () => {
  before(() => {
    cy.loginAndVisit('/admin/lists');
    // The people need the list IDs, so they are made once both lists exist.
    cy.request('POST', '/api/lists', { name: srcName, type: 'private', optin: 'single', tags: ['team-a'] }).then(({ body }) => {
      srcId = body.data.id;
      return cy.request('POST', '/api/lists', { name: exName, type: 'private', optin: 'single', tags: [] });
    }).then(({ body }) => {
      exId = body.data.id;
      Cypress._.times(people, (i) => {
        cy.request('POST', '/api/subscribers', {
          email: `draw-${stamp}-${i}@example.invalid`, name: `가상 ${i}`, status: 'enabled', attribs: { plan: 'pro' }, lists: i < excluded ? [srcId, exId] : [srcId],
        });
      });
    });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
    cy.loginAndVisit('/admin/lists');
    cy.location('pathname').should('eq', '/admin/lists');
  });

  it('checks the numbers, makes the list, and leaves the drawn people out of the next draw', () => {
    openList(srcName).find('[data-cy=btn-draw]').click();
    cy.get('[data-cy=list-draw]').should('be.visible');
    cy.get('[data-cy=draw-create]').should('be.disabled');

    // Nothing is counted for a value out of range.
    cy.get('[data-cy=draw-value]').type('0');
    cy.get('[data-cy=draw-check]').should('be.disabled');
    cy.get('[data-cy=draw-value]').clear().type('10');
    cy.get('[data-cy=draw-check]').click();
    cy.get('[data-cy=draw-target]').should('contain', String(people));
    cy.get('[data-cy=draw-already-drawn]').should('contain', '0');
    cy.get('[data-cy=draw-candidates]').should('contain', String(people));
    cy.get('[data-cy=draw-will]').should('contain', '10');
    cy.get('[data-cy=draw-name]').invoke('val').should('contain', srcName).and('contain', '10');

    // Changing the settings clears the numbers: the button asks for a new check.
    cy.get('[data-cy=draw-value]').clear().type('12');
    cy.get('[data-cy=draw-counts]').should('not.exist');
    cy.get('[data-cy=draw-create]').should('be.disabled');
    cy.get('[data-cy=draw-value]').clear().type('10');
    cy.get('[data-cy=draw-check]').click();
    cy.get('[data-cy=draw-will]').should('contain', '10');
    cy.get('[data-cy=draw-create]').click();
    cy.get('[data-cy=draw-done]').should('be.visible');
    cy.get('[data-cy=draw-done-count]').should('contain', '10');
    cy.get('[data-cy=draw-campaign]').should('be.visible');
    cy.get('[data-cy=draw-close]').click();

    cy.request(`/api/lists/draws?source_id=${srcId}`).then(({ body }) => {
      expect(body.data).to.have.length(1);
      const draw = body.data[0];
      expect(draw.result.drawn).to.eq(10);
      expect(draw.result.target).to.eq(people);
      cy.request(`/api/lists/${draw.list_id}`).then(({ body: list }) => {
        expect(list.data.type).to.eq('private');
        expect(list.data.optin).to.eq('single');
        expect(list.data.axm_filter).to.eq(null);
        expect(list.data.tags).to.include.members(['team-a', '뽑은-리스트']);
        expect(list.data.subscriber_count).to.be.at.least(10);
      });
    });

    // Asking for more than are left: the preview says so and the list takes what is left.
    openList(srcName).find('[data-cy=btn-draw]').click();
    cy.get('[data-cy=draw-value]').type('100');
    cy.get('[data-cy=draw-check]').click();
    cy.get('[data-cy=draw-already-drawn]').should('contain', '10');
    cy.get('[data-cy=draw-candidates]').should('contain', String(people - 10));
    cy.get('[data-cy=draw-short]').should('be.visible');
    cy.get('[data-cy=draw-create]').should('contain', String(people - 10));
    cy.get('[data-cy=draw-create]').click();
    cy.get('[data-cy=draw-done-count]').should('contain', String(people - 10));
    cy.get('[data-cy=draw-close]').click();

    // Everyone is drawn now: there is nobody, and the button stays off.
    openList(srcName).find('[data-cy=btn-draw]').click();
    cy.get('[data-cy=draw-unit]').select('percent');
    cy.get('[data-cy=draw-value]').type('50');
    cy.get('[data-cy=draw-check]').click();
    cy.get('[data-cy=draw-none]').should('be.visible');
    cy.get('[data-cy=draw-create]').should('be.disabled');
    cy.get('[data-cy=draw-close]').click();
  });

  it('marks drawn lists, filters to them, and shows the draw records of the source', () => {
    openList(srcName).find('[data-cy=btn-draw]').should('exist');
    openList(srcName).find('[data-cy=drawn-from]').should('not.exist');
    cy.get('[data-cy=query]').clear().type(srcName);
    cy.get('[data-cy=btn-query]').click();
    cy.get('[data-cy=drawn-from]').should('have.length', 2).first().should('contain', srcName);
    // Drawn lists have no draw action of their own.
    cy.get('tr').filter(':has([data-cy=drawn-from])').each(($tr) => {
      cy.wrap($tr).find('[data-cy=btn-draw]').should('not.exist');
    });

    cy.get('[data-cy=drawn-only]').click();
    // Only the two drawn lists are left, each marked.
    cy.get('tbody tr [data-cy=drawn-from]').should('have.length', 2);
    cy.get('tbody tr').should('have.length', 2);
    cy.get('[data-cy=drawn-all]').click();
    cy.get('[data-cy=drawn-only]').should('exist');

    // The source's edit form lists what was drawn, and a drawn list's form says where it came from.
    openList(srcName).find('[data-cy=btn-edit]').click();
    cy.get('[data-cy=draw-history] tbody tr').should('have.length', 2);
    cy.get('[data-cy=btn-draw-from-form]').should('exist');
    cy.get('[data-cy=draw-history] tbody tr').first().find('a').first().click();
    cy.get('[data-cy=draw-summary]').should('contain', srcName);
    cy.get('[data-cy=btn-draw-from-form]').should('not.exist');
  });

  it('keeps the people on an exclude list out of a dynamic list, with and without conditions', () => {
    const dynName = `Draw dynamic ${stamp}`;
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('input[name=name]').type(dynName);
    cy.get('[data-cy=dynamic-base]').select('lists');
    cy.get('[data-cy=dynamic-base-lists] input').type(srcName);
    cy.contains('[data-cy=dynamic-base-lists] .autocomplete a', srcName).click();
    cy.get('[data-cy=dynamic-exclude] input').type(exName);
    cy.contains('[data-cy=dynamic-exclude] .autocomplete a', exName).click();
    cy.get('[data-cy=dynamic-exclude] .list-tags').should('contain', exName);

    // With a condition, then with none at all.
    cy.get('[data-cy=dynamic-key-0]').type('plan');
    cy.get('[data-cy=dynamic-value-0]').type('pro');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', String(people - excluded));
    cy.get('[data-cy=dynamic-excluded]').should('contain', String(excluded));
    cy.get('[data-cy=dynamic-remove-0]').click();
    cy.get('[data-cy=dynamic-no-conditions]').scrollIntoView().should('be.visible');
    cy.get('[data-cy=dynamic-match]').should('not.exist');
    cy.get('[data-cy=dynamic-preview]').click();
    cy.get('[data-cy=dynamic-count]').should('contain', String(people - excluded));
    cy.get('[data-cy=dynamic-summary]').should('contain', exName);
    cy.get('[data-cy=btn-save]').click();

    cy.request({ url: '/api/lists', qs: { query: dynName } }).then(({ body }) => {
      const list = body.data.results.find((item) => item.name === dynName);
      expect(list.axm_filter.version).to.eq(2);
      expect(list.axm_filter.conditions).to.have.length(0);
      expect(list.axm_filter.exclude.lists).to.deep.eq([exId]);
      expect(list.subscriber_count).to.eq(people - excluded);
    });

    // The exclude list cannot be deleted while the dynamic list uses it.
    cy.request({ method: 'DELETE', url: `/api/lists/${exId}`, failOnStatusCode: false }).then((resp) => {
      expect(resp.status).to.eq(400);
    });

    // Taking the last exclude list away of a rule without conditions brings a blank condition back.
    openList(dynName).find('[data-cy=btn-edit]').click();
    cy.get('[data-cy=dynamic-exclude] .list-tags .tag .delete').click();
    cy.get('[data-cy=dynamic-condition-0]').should('be.visible');
    cy.get('[data-cy=dynamic-remove-0]').should('be.disabled');
    cy.get('.modal-card-foot button').contains(/close|닫기/i).click();
  });

  it('makes a list to exclude from the exclude list field and opens the import in a new tab', () => {
    const newName = `Draw new exclude ${stamp}`;
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-composition]').select('dynamic');
    cy.get('input[name=name]').type(`Draw shortcut ${stamp}`);
    cy.get('[data-cy=exclude-import]').should('be.visible');
    cy.get('[data-cy=exclude-new]').click();
    cy.get('[data-cy=exclude-new-name]').invoke('val').should('not.be.empty');
    cy.get('[data-cy=exclude-new-form]').should('contain', 'Private');
    cy.get('[data-cy=exclude-new-name]').clear().type(newName);
    cy.window().then((win) => cy.stub(win, 'open').as('open'));
    cy.get('[data-cy=exclude-new-import]').click();
    cy.get('[data-cy=exclude-made]').should('be.visible');
    cy.get('[data-cy=dynamic-exclude] .list-tags').should('contain', newName);
    cy.get('@open').should('have.been.calledOnce');
    cy.get('@open').its('firstCall.args.0').should('match', /list_id=\d+/);

    cy.request({ url: '/api/lists', qs: { query: newName } }).then(({ body }) => {
      const list = body.data.results.find((item) => item.name === newName);
      expect(list.type).to.eq('private');
      expect(list.optin).to.eq('single');
      expect(list.axm_filter).to.eq(null);
      cy.get('@open').its('firstCall.args.0').should('contain', `list_id=${list.id}`);
    });
    // The recount button and the import button of the made list.
    cy.get('[data-cy=exclude-recount]').should('be.visible');
    cy.get('[data-cy=exclude-made-import]').click();
    cy.get('@open').should('have.been.calledTwice');
  });
});
