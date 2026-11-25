const stamp = Date.now();
const name = `Recent exclusion ${stamp}`;
const listName = `Recent recipients ${stamp}`;
const password = 'local-recent-reader-password';
const username = `recent-reader-${stamp}`;
const preview = '/api/campaigns/recent-exclude/preview';
const switchInput = '[data-cy=recent-enabled] input';
const switchControl = '[data-cy=recent-enabled] .check';

let campaignId;
let optinId;
let sourceListId;

const openCampaign = (id) => cy.visit(`/admin/campaigns/${id}`);
const rulePath = () => `/api/campaigns/${campaignId}/recent-exclude`;

describe('Recent-send exclusion settings', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', {
      name: listName, type: 'private', optin: 'single', no_team: true,
    }).then(({ body }) => {
      sourceListId = body.data.id;
      cy.request('POST', '/api/subscribers', {
        name: 'Synthetic recipient',
        email: `recent-${stamp}@example.invalid`,
        status: 'enabled',
        lists: [body.data.id],
      });
    });
    cy.request('POST', '/api/lists', {
      name: `Opt-in ${stamp}`, type: 'private', optin: 'double', no_team: true,
    }).then(({ body }) => {
      cy.request('POST', '/api/campaigns', {
        name: `Opt-in campaign ${stamp}`, subject: 'Opt-in', type: 'optin', lists: [body.data.id],
      }).then(({ body: campaign }) => { optinId = campaign.data.id; });
    });
    cy.request('POST', '/api/roles/users', {
      name: `Recent reader ${stamp}`, permissions: ['campaigns:get_all', 'lists:get_all', 'templates:get'],
    }).then(({ body }) => {
      cy.request('POST', '/api/users', {
        username, type: 'user', name: 'Read only', email: `read-${stamp}@example.invalid`, status: 'enabled',
        password_login: true, password, user_role_id: body.data.id,
      });
    });
  });

  it('previews a disabled-by-default rule, updates the preview and saves on create', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns').as('createCampaign');
    cy.intercept('PUT', '/api/campaigns/*/recent-exclude').as('saveRule');
    openCampaign('new');
    cy.get(switchInput).should('not.be.checked');
    cy.get('[data-cy=recent-counts]').should('not.exist');
    cy.get('input[name=name]').type(name);
    cy.get('input[name=subject]').type('Preview example');
    cy.get('.list-selector input').click().type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    cy.get(switchControl).click();

    cy.wait('@recentPreview').its('request.body').should('include', { days: 14, scope: 'all' });
    cy.get('[data-cy=recent-counts]').find('td').then(($cells) => {
      expect([...$cells].map((cell) => cell.textContent)).to.deep.equal(['1', '−0', '1']);
    });
    cy.get('[data-cy=recent-as-of]').contains(/As of \d{4}-\d{2}-\d{2} \d{2}:\d{2}/);
    cy.get('[data-cy=recent-notice]').should('contain', 'left out when the campaign is sent');
    // The list of the result is a side action below the notice, not the main one (LST-65).
    cy.get('[data-cy=recent-same-basis]').should('contain', 'send window');

    cy.get('select[data-cy=recent-days]').select('7');
    cy.wait('@recentPreview').its('request.body.days').should('eq', 7);
    cy.get('[data-cy=recent-counts]').should('contain', 'last 7 days');
    cy.get('select[data-cy=recent-scope]').select('team');
    cy.wait('@recentPreview').its('response.statusCode').should('eq', 400);
    cy.get('[data-cy=recent-error]').should('contain', 'no team tags');
    cy.get('[data-cy=recent-counts]').should('not.exist');

    cy.get('select[data-cy=recent-scope]').select('all');
    cy.wait('@recentPreview').its('response.statusCode').should('eq', 200);
    cy.get('[data-cy=btn-continue]').click();
    cy.wait('@createCampaign').then(({ response }) => { campaignId = response.body.data.id; });
    cy.wait('@saveRule').its('response.body.data').should('include', { enabled: true, days: 7, scope: 'all' });
    cy.location('hash').should('eq', '#content');
  });

  it('keeps the rule locked and shows its holds when a started campaign is scheduled again', () => {
    cy.intercept('GET', `/api/campaigns/${campaignId}`, (req) => {
      req.continue((res) => {
        Object.assign(res.body.data, { status: 'scheduled', started_at: new Date().toISOString() });
      });
    });
    cy.intercept('GET', rulePath(), (req) => {
      req.continue((res) => {
        res.body.data.held = 1;
      });
    });
    openCampaign(campaignId);
    cy.get(switchInput).should('be.checked').and('be.disabled');
    cy.get('[data-cy=recent-held]').should('contain', '1');
  });

  it('keeps the completed audience when some sends fail and later mail reaches the same list', () => {
    cy.intercept('GET', `/api/campaigns/${campaignId}`, (req) => {
      req.continue((res) => {
        Object.assign(res.body.data, {
          status: 'finished', to_send: 40, sent: 27, started_at: new Date().toISOString(),
        });
      });
    });
    cy.intercept('GET', rulePath(), (req) => {
      req.continue((res) => {
        res.body.data.held = 12;
      });
    });
    cy.intercept('POST', preview, {
      body: { data: { target: 40, excluded: 40, will_send: 0 } },
    }).as('laterSendsPreview');
    openCampaign(campaignId);
    cy.get('[data-cy=recent-counts] td').should(($cells) => {
      expect([...$cells].map((cell) => cell.textContent)).to.deep.equal(['40', '−12', '28']);
    });
    cy.get('[data-cy=recent-as-of]').should('contain', 'As of');
    cy.contains('[data-cy=recent-exclude]', 'including addresses that later bounced').should('be.visible');
    cy.get('[data-cy=recent-held]').should('contain', '12');
    cy.get('@laterSendsPreview.all').should('have.length', 0);
  });

  it('loads a saved rule, warns on send and disables editing for a reader', () => {
    openCampaign(campaignId);
    cy.get(switchInput).should('be.checked').and('not.be.disabled');
    cy.get('select[data-cy=recent-days]').should('have.value', '7');
    cy.get('[data-cy=btn-start]').click();
    // The saved rule is applied by the sender (LST-65): the window says so, as information.
    cy.get('[data-cy="send-check-recent.applied"]').should('have.attr', 'data-level', 'info').and('contain', 'last 7 days');
    cy.get('[data-cy="send-check-recent.notApplied"]').should('not.exist');
    cy.get('[data-cy=send-cancel]').click();

    cy.clearCookies();
    cy.visit(`/admin/login?next=/admin/campaigns/${campaignId}`);
    cy.get('input[name=username]').type(username);
    cy.get('input[name=password]').type(password);
    cy.get('button[type=submit]').click();
    cy.get(switchInput).should('be.checked').and('be.disabled');
    cy.get('select[data-cy=recent-days]').should('be.disabled');
    cy.get('[data-cy=recent-make-list]').should('not.exist');
    cy.clearCookies();
    cy.loginAndVisit(`/admin/campaigns/${campaignId}`);
  });

  it('removes a saved rule when turned off and hides it for opt-in campaigns', () => {
    cy.intercept('PUT', '/api/campaigns/*/recent-exclude').as('saveRule');
    cy.get(switchInput).should('be.checked');
    cy.get(switchControl).click();
    cy.get('[data-cy=btn-save]').click();
    cy.wait('@saveRule').its('response.body.data').should('deep.equal', { enabled: false });
    cy.request(rulePath()).its('body.data').should('deep.equal', { enabled: false });
    cy.reload();
    cy.get(switchInput).should('not.be.checked');

    cy.intercept('GET', `/api/campaigns/${optinId}`).as('loadOptin');
    openCampaign(optinId);
    cy.wait('@loadOptin').its('response.body.data.type').should('eq', 'optin');
    cy.get('[data-cy=recent-exclude]').should('not.exist');
  });

  it('makes a list from the result, changes the target to it and saves the campaign', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns/recent-exclude/list').as('makeList');
    cy.intercept('PUT', `/api/campaigns/${campaignId}`).as('updateCampaign');
    openCampaign(campaignId);
    cy.get(switchInput).should('not.be.checked');
    cy.get('[data-cy=recent-make-list]').should('not.exist');
    cy.get(switchControl).click();
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();

    // The modal says each thing once: team, name and one note with the numbers.
    cy.get('[data-cy=recent-list-modal]').should('be.visible');
    cy.get('[data-cy=recent-list-create]').should('be.disabled');
    cy.get('select[data-cy=recent-list-team]').select('__none__');
    cy.get('input[data-cy=recent-list-name]').should(($input) => {
      expect($input.val()).to.contain(listName).and.to.contain('last 14 days');
    });
    cy.get('[data-cy=recent-list-note]').should('contain', '1 people').and('contain', '0 excluded');
    cy.get('[data-cy=recent-list-create]').click();

    cy.wait('@makeList').then(({ request, response }) => {
      // The name the person saw is the name that is sent.
      expect(request.body).to.include({ days: 14, scope: 'all', no_team: true, campaign_id: campaignId });
      expect(request.body.name).to.contain(listName).and.to.contain('last 14 days');
      expect(response.body.data.counts).to.deep.equal({ target: 1, excluded: 0, added: 1 });
      cy.wrap(response.body.data.list).as('made');
    });
    cy.wait('@updateCampaign').its('request.body.lists').should('have.length', 1);
    cy.get('[data-cy=recent-list-made]').should('contain', '✓').and('contain', 'saved').find('a').should('exist');
    cy.get('.toast').should('not.exist');
    cy.get('.list-selector .tags .tag').should('have.length', 1);
    cy.get('[data-cy=recent-make-list]').should('not.exist');

    cy.get('@made').then((made) => {
      cy.get('.list-selector .tags .tag').should('contain', made.name);
      cy.request(`/api/campaigns/${campaignId}`).its('body.data.lists').then((lists) => {
        expect(lists.map(({ id }) => id)).to.deep.equal([made.id]);
      });
      cy.request(`/api/lists/${made.id}`).its('body.data').should((list) => {
        expect(list.type).to.eq('private');
        expect(list.optin).to.eq('single');
        expect(list.tags).to.deep.equal(['제외-적용-리스트']);
      });
      cy.request(`/api/subscribers?list_id=${made.id}`).its('body.data.total').should('eq', 1);
    });
  });

  it('brings the button back when the preview shows somebody newly excluded', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns/recent-exclude/list').as('makeList');
    openCampaign(campaignId);
    cy.get(switchInput).should('be.checked');
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();
    cy.get('select[data-cy=recent-list-team]').select('__none__');
    cy.get('[data-cy=recent-list-create]').click();
    cy.wait('@makeList');
    cy.get('[data-cy=recent-list-made]').should('be.visible');
    cy.get('[data-cy=recent-make-list]').should('not.exist');

    // A later change of the rule shows somebody newly excluded: the result line gives way to the button.
    cy.intercept('POST', preview, {
      body: { data: { target: 2, excluded: 1, will_send: 1, as_of: new Date().toISOString(), records_from: null, team_tags: [] } },
    }).as('stubbed');
    cy.get('select[data-cy=recent-days]').select('30');
    cy.wait('@stubbed');
    cy.get('[data-cy=recent-make-list]').should('be.visible');
    cy.get('[data-cy=recent-list-made]').should('not.exist');
  });

  it('shows an error, not a saved line, when the campaign cannot be saved', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('PUT', `/api/campaigns/${campaignId}`, { statusCode: 400, body: { message: 'refused' } }).as('refused');
    openCampaign(campaignId);
    cy.get(switchInput).should('be.checked');
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();
    cy.get('select[data-cy=recent-list-team]').select('__none__');
    cy.get('[data-cy=recent-list-create]').click();
    cy.wait('@refused');
    cy.get('[data-cy=recent-list-save-error]').should('contain', 'The list was made');
    cy.get('[data-cy=recent-list-made]').should('not.exist');
    cy.get('[data-cy=recent-make-list]').should('not.exist');
  });

  it('keeps the made list as the target of a new campaign started from a list', () => {
    const makePath = '/api/campaigns/recent-exclude/list';
    let made = false;
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', makePath, (req) => {
      req.continue(() => { made = true; });
    }).as('makeList');
    // The lists are read again after the list is made; that read must not bring the ?list_id list back.
    cy.intercept('GET', '/api/lists*', (req) => {
      if (made) req.alias = 'listsAfter';
    });
    cy.intercept('POST', '/api/campaigns').as('createCampaign');
    cy.visit(`/admin/campaigns/new?list_id=${sourceListId}`);
    cy.get('.list-selector .tags .tag').should('have.length', 1).and('contain', listName);
    cy.get('input[name=name]').type(`${name} from a list`);
    cy.get('input[name=subject]').type('From a list');
    cy.get(switchControl).click();
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();
    cy.get('select[data-cy=recent-list-team]').select('__none__');
    cy.get('[data-cy=recent-list-create]').click();
    cy.wait('@makeList').its('response.body.data.list').then((list) => {
      cy.wait('@listsAfter');
      cy.get('.list-selector .tags .tag').should('have.length', 1).and('contain', list.name);
      cy.get('button[type=submit]').click();
      cy.wait('@createCampaign').then(({ request, response }) => {
        expect(request.body.lists).to.deep.equal([list.id]);
        cy.request(`/api/campaigns/${response.body.data.id}`).its('body.data.lists').then((lists) => {
          expect(lists.map(({ id }) => id)).to.deep.equal([list.id]);
        });
      });
    });
  });

  it('does not save a title discarded while the save of the made list waited for another save', () => {
    let puts = 0;
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns/recent-exclude/list', (req) => {
      req.continue((res) => { res.setDelay(1500); });
    }).as('makeList');
    cy.intercept('PUT', `/api/campaigns/${campaignId}`, (req) => {
      puts += 1;
      // The save pressed while the list is being made answers late.
      if (puts === 1) req.continue((res) => { res.setDelay(6000); });
    }).as('updateCampaign');
    cy.request(`/api/campaigns/${campaignId}`).its('body.data.name').then((title) => {
      openCampaign(campaignId);
      cy.get(switchInput).should('be.checked');
      cy.wait('@recentPreview');
      cy.get('[data-cy=recent-make-list]').click();
      cy.get('select[data-cy=recent-list-team]').select('__none__');
      cy.get('[data-cy=recent-list-create]').click();
      cy.get('body').trigger('keydown', { key: 's', ctrlKey: true });
      cy.wait('@makeList');
      // The held save keeps the page's loading cover up; the title changes underneath it.
      cy.get('input[name=name]').clear({ force: true }).type(`Discarded title ${stamp}`, { force: true });
      cy.get('[data-cy=recent-exclude]').then(($el) => {
        // eslint-disable-next-line no-underscore-dangle
        $el[0].__vue__.$router.push({ name: 'campaigns' }).catch(() => {});
      });
      // The loading cover of the held save sits over the dialog's middle.
      cy.get('.modal.dialog').should('exist').and('contain', 'Discard');

      // The first save answers while the person is still asked: nothing else is saved.
      cy.wait('@updateCampaign');
      cy.wait(1500).then(() => expect(puts).to.eq(1));
      cy.get('.modal.dialog .modal-card-foot .button.is-primary').click({ force: true });
      cy.location('pathname').should('eq', '/admin/campaigns');
      cy.wait(1500).then(() => expect(puts).to.eq(1));
      cy.request(`/api/campaigns/${campaignId}`).its('body.data.name').should('eq', title);
    });
  });

  it('says the target changed when only the rule failed to save', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns/recent-exclude/list').as('makeList');
    cy.intercept('PUT', `/api/campaigns/${campaignId}`).as('updateCampaign');
    cy.intercept('PUT', rulePath(), { statusCode: 503, body: { message: 'unavailable' } }).as('ruleRefused');
    openCampaign(campaignId);
    cy.get(switchInput).should('be.checked');
    cy.wait('@recentPreview');
    // A changed rule is saved after the campaign.
    cy.get('select[data-cy=recent-days]').select('30');
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();
    cy.get('select[data-cy=recent-list-team]').select('__none__');
    cy.get('[data-cy=recent-list-create]').click();
    cy.wait('@makeList').its('response.body.data.list').then((list) => {
      cy.wait('@updateCampaign');
      cy.wait('@ruleRefused');
      cy.contains('[role=alert]', 'exclusion rule failed to save').should('be.visible');
      cy.get('[data-cy=recent-list-save-error]').should('not.exist');
      cy.get('[data-cy=recent-list-made]').should('contain', 'saved');
      cy.request(`/api/campaigns/${campaignId}`).its('body.data.lists').then((lists) => {
        expect(lists.map(({ id }) => id)).to.deep.equal([list.id]);
      });
    });
  });

  it('fits the default name of a long source list in 200 characters', () => {
    const long = `${'L'.repeat(170)} ${stamp}`;
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns/recent-exclude/list').as('makeList');
    cy.request('POST', '/api/lists', {
      name: long, type: 'private', optin: 'single', no_team: true,
    }).then(({ body }) => {
      cy.request('POST', '/api/subscribers', {
        name: 'Long list recipient', email: `long-${stamp}@example.invalid`, status: 'enabled', lists: [body.data.id],
      });
      cy.visit(`/admin/campaigns/new?list_id=${body.data.id}`);
    });
    cy.get('.list-selector .tags .tag').should('have.length', 1);
    cy.get(switchControl).click();
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();
    cy.get('input[data-cy=recent-list-name]').should(($input) => {
      expect(Array.from($input.val()).length).to.eq(200);
      expect($input.val()).to.contain('last 14 days');
    });
    // No character counter: the approved dialog has none.
    cy.get('input[data-cy=recent-list-name]').click();
    cy.get('[data-cy=recent-list-modal] .counter').should('not.exist');
    cy.get('select[data-cy=recent-list-team]').select('__none__');
    cy.get('[data-cy=recent-list-create]').click();
    cy.wait('@makeList').its('response.statusCode').should('eq', 200);
  });

  it('keeps a team chosen while the teams are still loading', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('GET', '/api/lists/teams', (req) => {
      req.continue((res) => { res.setDelay(2500); });
    }).as('teams');
    openCampaign(campaignId);
    cy.get(switchInput).should('be.checked');
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();
    cy.get('select[data-cy=recent-list-team]').select('__none__', { force: true });
    cy.wait('@teams');
    cy.get('select[data-cy=recent-list-team]').should('have.value', '__none__');
    cy.get('[data-cy=recent-list-create]').should('not.be.disabled');
  });

  it('says a new campaign saved its target when only its rule failed to save', () => {
    cy.intercept('POST', preview).as('recentPreview');
    cy.intercept('POST', '/api/campaigns/recent-exclude/list').as('makeList');
    cy.intercept('POST', '/api/campaigns').as('createCampaign');
    cy.intercept('PUT', '/api/campaigns/*/recent-exclude', { statusCode: 503, body: { message: 'unavailable' } }).as('ruleRefused');
    cy.visit(`/admin/campaigns/new?list_id=${sourceListId}`);
    cy.get('.list-selector .tags .tag').should('have.length', 1);
    cy.get('input[name=name]').type(`${name} rule refused`);
    cy.get('input[name=subject]').type('Rule refused');
    cy.get(switchControl).click();
    cy.wait('@recentPreview');
    cy.get('[data-cy=recent-make-list]').click();
    cy.get('select[data-cy=recent-list-team]').select('__none__');
    cy.get('[data-cy=recent-list-create]').click();
    cy.wait('@makeList').its('response.body.data.list').then((list) => {
      cy.get('[data-cy=recent-list-made]').should('contain', 'when you create');
      cy.get('button[type=submit]').click();
      cy.wait('@createCampaign').its('response.body.data.id').then((id) => {
        cy.wait('@ruleRefused');
        cy.contains('[role=alert]', 'exclusion rule failed to save').should('be.visible');
        cy.get('[data-cy=recent-list-made]').should('contain', 'it is saved').and('not.contain', 'when you create');
        cy.request(`/api/campaigns/${id}`).its('body.data.lists').then((lists) => {
          expect(lists.map((l) => l.id)).to.deep.equal([list.id]);
        });
      });
    });
  });
});

