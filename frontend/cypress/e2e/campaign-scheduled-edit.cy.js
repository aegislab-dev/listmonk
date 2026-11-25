// A scheduled campaign that is saved is checked again (LST-67): a refused save when nobody is left
// to send to, the pre-send window with the warnings that remain, one line when all is well. Synthetic
// data only. A bare template is used: the default one has the unsubscribe link and the view pixel
// already, so every case would read as passed.
const stamp = Date.now();
const win = '[data-cy=send-confirm]';
const camp = {};
const oldOffer = 'Original offer: 5 percent';
const newOffer = 'Approved offer: 20 percent';
const oldFooter = 'Original footer';
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

const visualContent = (offer) => ({
  content_type: 'visual',
  template_id: 0,
  body: `<p>${offer}</p><p>${oldFooter}</p>`,
  body_source: JSON.stringify({
    root: { type: 'EmailLayout', data: { childrenIds: ['offer', 'footer'] } },
    offer: { type: 'Text', data: { props: { text: offer } } },
    footer: { type: 'Text', data: { props: { text: oldFooter } } },
  }),
});

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
      create('conflict', {});
      create('visualConflict', visualContent(oldOffer));
      create('draftDuringCheck', {});
      create('pass', {
        body: '<p>Hi <a href="https://example.invalid/x@TrackLink">x</a> <a href="{{ UnsubscribeURL }}">leave</a></p>{{ TrackView }}',
      });
      create('refuse', {});
      create('nolist', {});
      create('history', {});
      create('paused', {});
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

  it('keeps the later schedule and reloads the editor after an outdated unschedule request', () => {
    const path = `/api/campaigns/${camp.conflict}`;
    const newSubject = 'Subject saved by the second editor';
    const later = new Date(Date.now() + 86400000 * 67).toISOString();
    cy.intercept('GET', `${path}/preflight`).as('preflight');
    cy.intercept('PUT', `${path}/status`).as('unschedule');
    cy.visit(`/admin/campaigns/${camp.conflict}`);
    cy.get('[data-cy=btn-save]').click();
    cy.wait('@preflight').its('response.body.data.updated_at').as('seenVersion');
    cy.get(`${win}[data-state=ready][data-mode=scheduled]`);

    status('conflict').then((d) => cy.request('PUT', path, {
      ...d, lists: [listOk], media: [], subject: newSubject, send_at: later,
    }));
    cy.get('[data-cy=send-unschedule]').click();
    cy.wait('@unschedule').then(({ request, response }) => {
      expect(response.statusCode).to.equal(409);
      cy.get('@seenVersion').should('equal', request.body.updated_at);
    });
    cy.get(win).should('not.exist');
    cy.get('[name=subject]').should('have.value', newSubject);
    cy.get('[data-cy=btn-unschedule]').should('exist');
    cy.get('[name=name]').clear().type('Edited name only');
    cy.get('[data-cy=btn-save]').click();
    cy.get(`${win}[data-state=ready][data-mode=scheduled]`);
    status('conflict').then((d) => {
      expect(d.name).to.equal('Edited name only');
      expect(d.subject).to.equal(newSubject);
      expect(d.status).to.equal('scheduled');
      expect(new Date(d.send_at).getTime()).to.equal(new Date(later).getTime());
    });
  });

  it('loads the draft into the editor when another session unschedules during the check', () => {
    const path = `/api/campaigns/${camp.draftDuringCheck}`;
    let release;
    cy.intercept('GET', `${path}/preflight`, (req) => new Cypress.Promise((resolve) => {
      release = () => {
        req.continue();
        resolve();
      };
    })).as('preflight');
    cy.visit(`/admin/campaigns/${camp.draftDuringCheck}`);
    cy.get('[data-cy=btn-save]').click();
    cy.wrap(null).should(() => expect(release).to.be.a('function'));
    cy.request('PUT', `${path}/status`, { status: 'draft' });
    cy.then(() => release());
    cy.wait('@preflight').its('response.body.data.status').should('equal', 'draft');
    cy.get('[data-cy=btn-schedule]').should('exist');
    cy.get('[data-cy=btn-unschedule]').should('not.exist');
    cy.get(win).should('not.exist');
  });

  it('preserves the other editor’s visual block when only the footer is edited after a conflict', () => {
    const path = `/api/campaigns/${camp.visualConflict}`;
    const newFooter = 'Updated footer only';
    const checkOffer = (offer) => cy.get('#visual-editor').should(($iframe) => {
      expect($iframe[0].contentDocument?.body?.textContent || '').to.contain(offer);
    });
    cy.intercept('PUT', `${path}/status`).as('unschedule');
    cy.visit(`/admin/campaigns/${camp.visualConflict}#content`);
    checkOffer(oldOffer);
    cy.get('[data-cy=btn-save]').click();
    cy.get(`${win}[data-state=ready][data-mode=scheduled]`);

    status('visualConflict').then((d) => cy.request('PUT', path, {
      ...d, ...visualContent(newOffer), lists: [listOk], media: [], subject: 'Updated visual subject',
    }));
    cy.get('[data-cy=send-unschedule]').click();
    cy.wait('@unschedule').its('response.statusCode').should('equal', 409);
    cy.get(win).should('not.exist');
    checkOffer(newOffer);

    cy.get('#visual-editor').its('0.contentDocument.body').then((body) => {
      cy.wrap(body).contains(oldFooter).click();
      cy.wrap(body).find('[contenteditable=true]:visible').first().clear().type(newFooter);
    });
    cy.get('[data-cy=btn-save]').click();
    cy.get(`${win}[data-state=ready][data-mode=scheduled]`);
    status('visualConflict').then((d) => {
      const source = JSON.parse(d.body_source);
      expect(d.status).to.equal('scheduled');
      expect(d.subject).to.equal('Updated visual subject');
      expect(d.body).to.include(newOffer).and.to.include(newFooter).and.not.to.include(oldOffer);
      expect(source.offer.data.props.text).to.equal(newOffer);
      expect(source.footer.data.props.text).to.equal(newFooter);
    });
  });

  it('says that all is well in one line, with no window', () => {
    cy.visit(`/admin/campaigns/${camp.pass}`);
    cy.get('[data-cy=btn-save]').click();
    cy.get('.toast').should('contain', 'checks passed').and('contain', '2 people');
    cy.get(win).should('not.exist');
    status('pass').its('status').should('equal', 'scheduled');
  });

  it('marks incomplete send history as a minimum in the review window', () => {
    cy.intercept('GET', `/api/campaigns/${camp.history}/preflight`, (req) => {
      req.continue((res) => {
        Object.assign(res.body.data, {
          resumed: true, remaining: 1, sent: 21, to_send: 30, sent_is_minimum: true,
        });
      });
    });
    cy.visit(`/admin/campaigns/${camp.history}`);
    cy.get('[data-cy=btn-save]').click();
    cy.get(`${win}[data-state=ready][data-mode=scheduled]`);
    cy.get('[data-cy=send-progress-text]').should('contain', '≥21 sent of 30');
    cy.get('[data-cy=send-progress]').should('contain', 'Recipients whose send records were deleted may receive this email again.');
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

  it('says there is no list when the last one is taken off', () => {
    cy.visit(`/admin/campaigns/${camp.nolist}`);
    cy.get(`.list-selector .tag[data-id=${listOk}] .delete`).click();
    cy.get('[data-cy=btn-save]').click();
    cy.get('.toast.is-danger').should('contain', 'no list to send to');
    status('nolist').then((c) => {
      expect(c.status).to.equal('scheduled');
      expect(c.lists.map((l) => l.id)).to.deep.equal([listOk]);
    });
  });

  it('does not open the window for a draft', () => {
    cy.request('PUT', `/api/campaigns/${camp.tail}/status`, { status: 'draft' });
    cy.visit(`/admin/campaigns/${camp.tail}`);
    cy.get('[data-cy=btn-save]').click();
    cy.get('.toast').should('contain', 'updated');
    cy.get(win).should('not.exist');
  });

  it('does not recheck a paused save', () => {
    const path = `/api/campaigns/${camp.paused}`;
    cy.request('PUT', `${path}/status`, { status: 'draft' });
    // The Go test saves a real paused campaign. Return that status here without starting the sender.
    ['GET', 'PUT'].forEach((method) => {
      cy.intercept(method, path, (req) => {
        req.continue((res) => { res.body.data.status = 'paused'; });
      });
    });
    cy.intercept('GET', `${path}/preflight`).as('recheck');
    cy.visit(`/admin/campaigns/${camp.paused}`);
    cy.get('[data-cy=btn-save]').click();
    cy.get('.toast').should('contain', 'updated');
    cy.get(win).should('not.exist');
    cy.get('@recheck.all').should('have.length', 0);
  });
});
