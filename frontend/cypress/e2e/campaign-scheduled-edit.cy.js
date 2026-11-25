// A scheduled campaign that is saved is checked again (LST-67): a refused save when nobody is left
// to send to, the pre-send window with the warnings that remain, one line when all is well. Synthetic
// data only. A bare template is used: the default one has the unsubscribe link and the view pixel
// already, so every case would read as passed.
const stamp = Date.now();
const win = '[data-cy=send-confirm]';
const camp = {};
let listOk;
let listEmpty;
let tplBare;

const create = (key, data) => cy.request('POST', '/api/campaigns', {
  name: `Resched ${key} ${stamp}`,
  subject: `Resched ${key} subject`,
  type: 'regular',
  messenger: 'email',
  content_type: 'html',
  from_email: 'Resched <resched@crevasse.ai>',
  template_id: tplBare,
  lists: [listOk],
  // About two months ahead (the page asks for a time that is ahead).
  send_at: new Date(Date.now() + 86400000 * 60).toISOString(),
  body: '<p>No way to leave here <a href="https://example.invalid/offer">offer</a></p>',
  ...data,
}).then(({ body }) => {
  camp[key] = body.data.id;
  return cy.request('PUT', `/api/campaigns/${body.data.id}/status`, { status: 'scheduled' });
});

const status = (key) => cy.request(`/api/campaigns/${camp[key]}`).its('body.data');

describe('Saving a scheduled campaign', () => {
  before(() => {
    cy.loginAndVisit('/admin/lists');
    cy.request('POST', '/api/templates', {
      name: `Resched bare ${stamp}`, type: 'campaign', subject: '', body: '<html><body>{{ template "content" . }}</body></html>',
    }).then(({ body }) => { tplBare = body.data.id; });
    ['Ok', 'Empty'].forEach((n) => {
      cy.request('POST', '/api/lists', {
        name: `Resched ${n} ${stamp}`, type: 'private', optin: 'single', no_team: true,
      }).then(({ body }) => {
        if (n === 'Ok') listOk = body.data.id;
        else listEmpty = body.data.id;
      });
    });
    cy.then(() => {
      [1, 2].forEach((n) => cy.request('POST', '/api/subscribers', {
        email: `resched-${n}-${stamp}@example.invalid`, name: `Resched ${n}`, status: 'enabled', lists: [listOk], preconfirm_subscriptions: true,
      }));
    });
    cy.then(() => {
      create('warn', {});
      create('undo', {});
      create('pass', {
        body: '<p>Hi <a href="https://example.invalid/x@TrackLink">x</a> <a href="{{ UnsubscribeURL }}">leave</a></p>{{ TrackView }}',
      });
      create('refuse', {});
      create('tail', {});
    });
  });

  it('shows the warnings that are left, and keeps the schedule on OK', () => {
    cy.visit(`/admin/campaigns/${camp.warn}`);
    cy.get('[data-cy=btn-save]').click();

    cy.get(`${win}[data-state=ready][data-mode=scheduled]`);
    cy.get('[data-cy=send-title]').should('contain', 'Scheduled · sends to 2 people');
    cy.get('[data-cy=send-sub]').should('contain', 'Saved.').and('contain', 'warnings below');
    cy.get('[data-cy=send-check-unsubscribe\\.missing]').should('have.attr', 'data-level', 'warn');
    cy.get('[data-cy=send-schedule]').should('exist');
    // Nothing to start: no "send anyway" button, the other one takes the schedule off.
    cy.get('[data-cy=send-ok]').should('contain', 'OK — keep the schedule');
    cy.get('[data-cy=send-unschedule]').should('exist');

    cy.intercept('PUT', '/api/campaigns/*/status').as('status');
    cy.get('[data-cy=send-ok]').click();
    cy.get(win).should('not.exist');
    cy.get('@status.all').should('have.length', 0);
    status('warn').its('status').should('equal', 'scheduled');
  });

  it('takes the schedule off from the same window, back to a draft', () => {
    cy.visit(`/admin/campaigns/${camp.undo}`);
    cy.get('[data-cy=btn-save]').click();
    cy.get(`${win}[data-state=ready][data-mode=scheduled]`);
    cy.get('[data-cy=send-unschedule]').click();
    cy.get(win).should('not.exist');
    status('undo').its('status').should('equal', 'draft');
    // The page shows it as a draft: it can be scheduled again.
    cy.get('[data-cy=btn-schedule]').should('exist');
    cy.get('[data-cy=btn-unschedule]').should('not.exist');
  });

  it('says that all is well in one line, with no window', () => {
    cy.visit(`/admin/campaigns/${camp.pass}`);
    cy.get('[data-cy=btn-save]').click();
    cy.get('.toast').should('contain', 'checks passed').and('contain', '2 people');
    cy.get(win).should('not.exist');
    status('pass').its('status').should('equal', 'scheduled');
  });

  it('refuses a save that leaves nobody to send to, and keeps what was saved', () => {
    cy.visit(`/admin/campaigns/${camp.refuse}`);
    cy.get('.list-selector .tag[data-id=' + listOk + '] .delete').click();
    cy.get('.list-selector input').click().type(`Resched Empty ${stamp}`);
    cy.get('.list-selector .autocomplete a').contains(`Resched Empty ${stamp}`).click();
    cy.get('[data-cy=btn-save]').click();

    cy.get('.toast.is-danger').should('contain', 'Not saved').and('contain', '0 recipients');
    cy.get(win).should('not.exist');
    status('refuse').then((c) => {
      expect(c.status).to.equal('scheduled');
      expect(c.lists.map((l) => l.id)).to.deep.equal([listOk]);
    });
    // After a reload the old list is still there.
    cy.reload();
    cy.get(`.list-selector .tag[data-id=${listOk}]`).should('exist');
  });

  it('does not open the window for a draft', () => {
    cy.request('PUT', `/api/campaigns/${camp.tail}/status`, { status: 'draft' });
    cy.visit(`/admin/campaigns/${camp.tail}`);
    cy.get('[data-cy=btn-save]').click();
    cy.get('.toast').should('contain', 'updated');
    cy.get(win).should('not.exist');
  });
});
