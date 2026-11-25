// Disposable synthetic-data coverage for the team screens (LST-24): teams, a team member's
// own lists, the team field of a list, the other-team mark on a subscriber and the team role
// on the roles screen. No campaign is sent.
const stamp = Date.now();
// The names have no spaces: a space turns into a dash in the tag, and the screens show the tag's name.
const teamA = `시험A팀${stamp}`;
const teamB = `시험B팀${stamp}`;
const tagA = `팀:${teamA}`;
const tagB = `팀:${teamB}`;
const memberUser = `member${stamp}`;
const memberPass = 'member-pass-1234';
let memberRoleId;
let memberId;
let opRoleId;
let opId;
const opUser = `op${stamp}`;
let listAId;
let listBId;

const loginAs = (user, pass, next) => {
  cy.visit(`/admin/login?next=${next}`);
  cy.get('input[name=username]').invoke('val', user);
  cy.get('input[name=password]').invoke('val', pass);
  cy.get('button').click();
};

describe('teams', () => {
  before(() => {
    cy.loginAndVisit('/admin/users/teams');
    cy.request('POST', '/api/roles/users', {
      name: `팀원 시험 ${stamp}`,
      permissions: ['subscribers:get', 'subscribers:manage', 'campaigns:get', 'bounces:get'],
    }).then(({ body }) => { memberRoleId = body.data.id; });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
  });

  // Teams make every list need a team: leave the database as found for the other specs.
  after(() => {
    cy.loginAndVisit('/admin/lists');
    cy.request('/api/teams').then(({ body }) => {
      const mine = body.data.filter((t) => [teamA, teamB].includes(t.name));
      mine.forEach((t) => cy.request('PUT', `/api/teams/${t.id}/members`, { user_ids: [] }));
      cy.request('DELETE', `/api/users/${memberId}`);
      cy.request('DELETE', `/api/users/${opId}`);
      cy.request('DELETE', `/api/roles/${opRoleId}`);
      cy.request('/api/lists?per_page=all').then(({ body: lists }) => {
        const ids = lists.data.results.filter((l) => l.name.endsWith(`${stamp}`)).map((l) => l.id);
        ids.forEach((id) => cy.request('DELETE', `/api/lists/${id}`));
      });
      cy.request('/api/subscribers?per_page=all&query=' + encodeURIComponent(`subscribers.email LIKE 'shared-${stamp}%'`)).then(({ body: subs }) => {
        subs.data.results.forEach((sub) => cy.request('DELETE', `/api/subscribers/${sub.id}`));
      });
      mine.forEach((t) => cy.request('DELETE', `/api/teams/${t.id}`));
      cy.request('DELETE', `/api/roles/${memberRoleId}`);
    });
  });

  it('makes a team on the teams screen and adds a member', () => {
    cy.loginAndVisit('/admin/users/teams');
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=team-new-note]').should('be.visible');
    cy.get('[data-cy=team-name-input]').type(teamA);
    cy.get('[data-cy=team-user-role]').select(`팀원 시험 ${stamp}`);
    cy.get('[data-cy=btn-save]').click();
    cy.contains('tr', teamA).should('contain', tagA);

    // A second team by the API, then a member through the members screen.
    cy.request('POST', '/api/teams', { name: teamB, user_role_id: memberRoleId });
    cy.request('POST', '/api/users', {
      username: memberUser, email: `${memberUser}@example.invalid`, name: `팀원 ${stamp}`, type: 'user', status: 'enabled',
      password_login: true, password: memberPass, password2: memberPass, user_role_id: memberRoleId,
    }).then(({ body }) => { memberId = body.data.id; });
    cy.reload();
    cy.contains('tr', teamA).find('[data-cy=btn-members]').click();
    cy.get(`[data-cy=member-${memberUser}] input`).check({ force: true });
    cy.get('[data-cy=team-members] [data-cy=btn-save]').click();
    cy.contains('tr', teamA).find('[data-cy=team-member]').should('have.length', 1);
  });

  it('asks an admin for the team of a new list, and shows it as a badge and a filter', () => {
    cy.loginAndVisit('/admin/lists');
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-team-field]').should('be.visible');
    cy.get('[data-cy=list-team]').should('have.attr', 'required');
    cy.get('[data-cy=list-team-help]').should('have.class', 'is-danger');
    cy.get('input[name=name]').type(`A 리스트 ${stamp}`);
    cy.get('[data-cy=list-team]').select(teamA);
    cy.get('[data-cy=btn-save]').click();
    cy.contains('tr', `A 리스트 ${stamp}`).find('[data-cy=team-badge]').should('contain', teamA);
    cy.request('/api/lists?per_page=all&tag=' + encodeURIComponent(tagA)).then(({ body }) => {
      listAId = body.data.results[0].id;
    });

    cy.request('POST', '/api/lists', { name: `B 리스트 ${stamp}`, type: 'private', optin: 'single', tags: [tagB] }).then(({ body }) => {
      listBId = body.data.id;
    });
    cy.request({
      method: 'POST', url: '/api/lists', body: { name: `팀 없음 ${stamp}`, type: 'private', optin: 'single', tags: [] }, failOnStatusCode: false,
    }).its('status').should('eq', 400);
    cy.request('POST', '/api/lists', {
      name: `팀 없음 ${stamp}`, type: 'private', optin: 'single', tags: [], no_team: true,
    });

    cy.visit('/admin/lists');
    cy.get('[data-cy=team-filter]').select(tagB);
    cy.contains('tr', `B 리스트 ${stamp}`).should('exist');
    cy.contains('tr', `A 리스트 ${stamp}`).should('not.exist');
    cy.get('[data-cy=team-filter]').select('');
    cy.get('[data-cy=teamless-link]').click();
    cy.contains('tr', `팀 없음 ${stamp}`).should('exist');
    cy.contains('tr', `A 리스트 ${stamp}`).should('not.exist');
  });

  it('keeps a team role read only on the list roles screen', () => {
    cy.loginAndVisit('/admin/users/roles/lists');
    cy.get('[data-cy=team-roles-note]').should('be.visible');
    cy.contains('tr', teamA).find('[data-cy=team-role-tag]').should('exist');
    cy.contains('tr', teamA).find('[data-cy=btn-edit]').should('not.exist');
    cy.contains('tr', teamA).find('[data-cy=btn-team-manage]').should('exist');
  });

  it('shows a team member only their own team, lets them make a list of it, and marks shared subscribers', () => {
    // A subscriber on a list of each team.
    cy.request('POST', '/api/subscribers', {
      email: `shared-${stamp}@example.invalid`, name: `공유 ${stamp}`, status: 'enabled', lists: [listAId, listBId],
      attribs: { customer_id: 'c-1', follower_count: 12 },
    });
    // Somebody on the member's list only, and somebody who unsubscribed from the other team's list.
    cy.request('POST', '/api/subscribers', {
      email: `shared-${stamp}-own@example.invalid`, name: `우리 팀만 ${stamp}`, status: 'enabled', lists: [listAId],
    });
    cy.request('POST', '/api/subscribers', {
      email: `shared-${stamp}-unsub@example.invalid`, name: `해지 기록 ${stamp}`, status: 'enabled', lists: [listAId, listBId],
    }).then(({ body }) => cy.request('PUT', '/api/subscribers/lists', {
      ids: [body.data.id], action: 'unsubscribe', target_list_ids: [listBId],
    }));

    loginAs(memberUser, memberPass, '/admin/lists');
    cy.get('[data-cy=my-team]').should('contain', teamA);
    cy.contains('tr', `A 리스트 ${stamp}`).should('exist');
    cy.contains('tr', `B 리스트 ${stamp}`).should('not.exist');
    cy.get('[data-cy=team-filter]').should('not.exist');

    // The member makes a list: the team is fixed, and the list shows up at once.
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-team-fixed]').should('contain', teamA);
    cy.get('input[name=name]').type(`멤버 리스트 ${stamp}`);
    cy.get('[data-cy=btn-save]').click();
    cy.contains('tr', `멤버 리스트 ${stamp}`).find('[data-cy=team-badge]').should('contain', teamA);

    // A shared subscriber carries the mark; the other team's list name is not on the page.
    cy.visit('/admin/subscribers');
    cy.contains('tr', `shared-${stamp}@example.invalid`).find('[data-cy=other-teams]').should('contain', teamB);
    cy.contains('B 리스트').should('not.exist');

    // The shared person is locked for the member (LST-42): a lock where the delete button is,
    // and the person who is on the member's lists only keeps the button.
    cy.contains('tr', `shared-${stamp}@example.invalid`).find('[data-cy=btn-delete]').should('not.exist');
    cy.contains('tr', `shared-${stamp}@example.invalid`).find('[data-cy=btn-delete-locked]').should('exist');
    cy.contains('tr', `shared-${stamp}-own@example.invalid`).find('[data-cy=btn-delete-locked]').should('not.exist');
    cy.contains('tr', `shared-${stamp}-own@example.invalid`).find('[data-cy=btn-delete]').should('exist');
    // Somebody with only an unsubscribe record on the other team's list is locked too, with a mark of its own.
    cy.contains('tr', `shared-${stamp}-unsub@example.invalid`).find('[data-cy=locked-record]').should('exist');
    cy.contains('tr', `shared-${stamp}-unsub@example.invalid`).find('[data-cy=btn-delete-locked]').should('exist');
  });

  it('locks the profile of a shared subscriber in the edit form and still saves the member\'s lists', () => {
    cy.contains('tr', `shared-${stamp}@example.invalid`).find('[data-cy=btn-edit]').click();
    cy.get('[data-cy=locked-notice]').should('be.visible').and('contain', teamB);
    cy.get('.modal-card input[name=email]').should('be.disabled');
    cy.get('.modal-card input[name=name]').should('be.disabled');
    cy.get('.modal-card select[name=status]').should('be.disabled');
    cy.get('.modal-card textarea[name=attribs]').should('be.disabled');

    // The member's own lists stay open: add the list the member made, and save.
    cy.get('.modal-card .list-selector input').type(`멤버 리스트 ${stamp}`);
    cy.contains('.modal-card .list-selector .autocomplete a', `멤버 리스트 ${stamp}`).click();
    cy.get('.modal-card-foot button[type=submit]').click();
    cy.get('.modal-card').should('not.exist');
    cy.contains('tr', `shared-${stamp}@example.invalid`).should('contain', `멤버 리스트 ${stamp}`);
    cy.contains('tr', `shared-${stamp}@example.invalid`).find('[data-cy=other-teams]').should('contain', teamB);

    // The server refuses what the form does not offer.
    cy.request('/api/subscribers?per_page=all').then(({ body }) => {
      const sub = body.data.results.find((r) => r.email === `shared-${stamp}@example.invalid`);
      expect(sub.edit_locked).to.eq(true);
      expect(body.data.results.filter((r) => r.id === sub.id)).to.have.length(1);
      cy.request({ method: 'DELETE', url: `/api/subscribers/${sub.id}`, failOnStatusCode: false }).its('status').should('eq', 403);
      cy.request({ method: 'PUT', url: `/api/subscribers/${sub.id}/blocklist`, failOnStatusCode: false }).its('status').should('eq', 403);
      cy.request({ method: 'PATCH', url: `/api/subscribers/${sub.id}`, body: { name: 'renamed' }, failOnStatusCode: false })
        .its('status').should('eq', 403);

      // Opened by its own link: the attribute keys stay as they are stored, and saving the
      // lists goes through without touching the profile.
      cy.visit(`/admin/subscribers/${sub.id}`);
      cy.get('[data-cy=locked-notice]').should('be.visible');
      cy.get('.modal-card textarea[name=attribs]').invoke('val').should('contain', 'customer_id').and('not.contain', 'customerId');
      cy.intercept('PATCH', `/api/subscribers/${sub.id}`).as('saveLists');
      cy.get('.modal-card-foot button[type=submit]').click();
      cy.wait('@saveLists').then(({ request, response }) => {
        expect(response.statusCode).to.eq(200);
        expect(request.body).to.have.property('lists');
        expect(request.body).to.not.have.any.keys('email', 'name', 'status', 'attribs');
      });
      cy.request(`/api/subscribers/${sub.id}`).then(({ body: one }) => {
        expect(one.data.attribs).to.deep.eq({ customer_id: 'c-1', follower_count: 12 });
      });
    });
  });

  it('leaves shared subscribers out of a bulk blocklist and says how many', () => {
    cy.visit('/admin/subscribers');
    const pick = (email) => cy.contains('tr', email).find('td.checkbox-cell .checkbox').click();

    // Only shared people: nothing to do, one button.
    pick(`shared-${stamp}@example.invalid`);
    pick(`shared-${stamp}-unsub@example.invalid`);
    cy.get('[data-cy=btn-manage-blocklist]').click();
    cy.get('[data-cy=warn-box]').should('be.visible');
    cy.get('.dialog .modal-card-foot button').should('have.length', 1).click();

    // With one of the member's own people: the dialog counts the one who goes and the two left out.
    pick(`shared-${stamp}-own@example.invalid`);
    cy.get('[data-cy=btn-manage-blocklist]').click();
    cy.get('.dialog .modal-card-title').should('contain', '1');
    cy.get('[data-cy=warn-box]').should('contain', '3').and('contain', '2');
    cy.get('.dialog .modal-card-foot button.is-danger').click();
    cy.contains('tr', `shared-${stamp}-own@example.invalid`).find('[data-cy=blocklisted]').should('exist');
    cy.contains('tr', `shared-${stamp}@example.invalid`).find('[data-cy=blocklisted]').should('not.exist');
    cy.contains('tr', `shared-${stamp}-unsub@example.invalid`).find('[data-cy=blocklisted]').should('not.exist');
  });

  it('lets an operator who may make every list but not manage roles choose the team', () => {
    cy.loginAndVisit('/admin/lists');
    cy.request('POST', '/api/roles/users', {
      name: `운영 시험 ${stamp}`,
      permissions: ['lists:get_all', 'lists:manage_all', 'subscribers:get_all', 'subscribers:manage'],
    }).then(({ body }) => {
      opRoleId = body.data.id;
      return cy.request('POST', '/api/users', {
        username: opUser, email: `${opUser}@example.invalid`, name: `운영 ${stamp}`, type: 'user', status: 'enabled',
        password_login: true, password: memberPass, password2: memberPass, user_role_id: opRoleId,
      });
    }).then(({ body }) => { opId = body.data.id; });

    loginAs(opUser, memberPass, '/admin/lists');
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=list-team-field]').should('be.visible');
    cy.get('input[name=name]').type(`운영 리스트 ${stamp}`);
    cy.get('[data-cy=list-team]').select(teamA);
    cy.get('[data-cy=btn-save]').click();
    cy.contains('tr', `운영 리스트 ${stamp}`).find('[data-cy=team-badge]').should('contain', teamA);
  });

  // LST-68: a default role keeps no permission that shows other teams' data.
  it('keeps roles with team-wide permissions out of the default role and says why', () => {
    const wideName = `전사 시험 ${stamp}`;
    let wideId;
    cy.loginAndVisit('/admin/users/teams');
    cy.request('POST', '/api/roles/users', {
      name: wideName, permissions: ['subscribers:get', 'subscribers:get_all'],
    }).then(({ body }) => { wideId = body.data.id; });
    cy.reload();
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=team-user-role] option').then(($o) => {
      const names = [...$o].map((o) => o.textContent.trim());
      expect(names).to.include(`팀원 시험 ${stamp}`);
      expect(names.some((n) => n.includes(wideName))).to.eq(false);
    });
    cy.get('[data-cy=team-form] [data-cy=btn-save]').should('exist');

    // The API refuses it too, and the message names the permissions.
    cy.request({
      method: 'POST', url: '/api/teams', body: { name: `시험C팀${stamp}`, user_role_id: wideId }, failOnStatusCode: false,
    }).then((res) => {
      expect(res.status).to.eq(400);
      expect(res.body.message).to.contain('subscribers:get_all').and.to.contain('campaigns:get_all');
    });
    // The roles screen refuses to give a team's role such a permission.
    cy.request({
      method: 'PUT',
      url: `/api/roles/users/${memberRoleId}`,
      body: { name: `팀원 시험 ${stamp}`, permissions: ['subscribers:get', 'campaigns:get_all'] },
      failOnStatusCode: false,
    }).then((res) => {
      expect(res.status).to.eq(400);
      expect(res.body.message).to.contain(teamA).and.to.contain('campaigns:get_all');
    });
    cy.request('DELETE', `/api/roles/${wideId}`);
  });

  it('warns on the teams screen about a team whose role shows other teams\' data', () => {
    // The API cannot make such a team any more, so the answer is replaced.
    cy.intercept('GET', '/api/teams', (req) => {
      req.continue((res) => {
        res.body.data.forEach((t) => {
          if (t.name === teamA) {
            t.wide = ['lists:get_all', 'lists:manage_all', 'subscribers:get_all'];
            t.wide_members = 1;
          }
        });
      });
    });
    cy.loginAndVisit('/admin/users/teams');
    cy.contains('tr', teamA).find('[data-cy=team-wide-tag]').should('be.visible');
    cy.contains('tr', teamA).find('[data-cy=team-wide-fix]').should('have.attr', 'href').and('contain', '/users/roles/users');
    cy.contains('tr', teamB).find('[data-cy=team-wide]').should('not.exist');
  });

  it('tells a team member that the bounce screen shows only their own lists', () => {
    loginAs(memberUser, memberPass, '/admin/subscribers/bounces');
    cy.get('[data-cy=bounces-scoped]').should('be.visible');
    cy.request('/api/bounces').its('status').should('eq', 200);
    cy.loginAndVisit('/admin/subscribers/bounces');
    cy.get('[data-cy=bounces-scoped]').should('not.exist');
  });
});
