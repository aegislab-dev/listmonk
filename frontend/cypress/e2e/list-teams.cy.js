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
    });

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

    // Deleting that person asks with the approved warning: a title, the other team, a delete button.
    cy.contains('tr', `shared-${stamp}@example.invalid`).find('[data-cy=btn-delete]').click();
    // (The checks do not depend on the language of the screen.)
    cy.get('.dialog .modal-card-title').invoke('text').should('not.be.empty');
    cy.get('[data-cy=warn-box]').should('contain', teamB);
    cy.get('.dialog .modal-card-foot button').should('have.length', 2);
    cy.get('.dialog .modal-card-foot button.is-danger').should('have.length', 1);
    cy.get('.dialog .modal-card-foot button').first().click();
    cy.contains('tr', `shared-${stamp}@example.invalid`).should('exist');
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
});
