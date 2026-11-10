// All data is synthetic. These tests use an already running disposable server;
// never call resetDB (its legacy helper kills processes and installs a new DB).
const storageKey = 'listmonk.axm.importMapping.v1';
const submit = () => cy.get('button[type=submit]');
const fileInput = () => cy.get('input[type=file]');
let listId;
let listName;

function chooseFile(fixture) {
  fileInput().selectFile(`cypress/fixtures/${fixture}`, { force: true });
}
function inlineFile(contents, name = 'synthetic.csv') {
  fileInput().selectFile({ contents: Cypress.Buffer.from(contents), fileName: name, mimeType: 'text/csv' }, { force: true });
}
function ready(count) {
  cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
  cy.get('[data-cy=map-summary]').should('contain', `${count}`);
  submit().should('not.be.disabled');
}
function setKey(index, value) {
  cy.get('[data-cy=map-busy]', { timeout: 30000 }).should('not.exist');
  cy.get('[data-cy=map-sheet]').scrollTo(index * 220, 0, { ensureScrollable: false });
  cy.get(`[data-cy=map-key-${index}]`).clear();
  cy.get(`[data-cy=map-key-${index}]`).focus().type(value, { scrollBehavior: false });
}
function importRows(count) {
  ready(count);
  submit().click();
  cy.get('.status .has-text-success', { timeout: 120000 }).should('contain', 'finished');
  cy.request('/api/import/subscribers').its('body.data.imported').should('eq', count);
}
function subscriber(email) {
  return cy.request({ url: '/api/subscribers', qs: { query: `email = '${email}'`, per_page: 1 } })
    .then(({ body }) => { expect(body.data.results).to.have.length(1); return body.data.results[0]; });
}

describe('CSV column mapping with the real Worker and import API', () => {
  before(() => {
    cy.loginAndVisit('/admin/subscribers/import');
    cy.location('pathname').should('eq', '/admin/subscribers/import');
    listName = `CSV synthetic ${Date.now()}`;
    cy.request('POST', '/api/lists', { name: listName, type: 'private', optin: 'single', tags: [] }).then(({ body }) => {
      listId = body.data.id;
      cy.request('POST', '/api/subscribers', {
        email: `key-sample-${listId}@example.invalid`, name: '가상 키 샘플', status: 'enabled', lists: [listId],
        attribs: { 'company-name': '가상 값', 회사명: '가상 값', company_name: '가상 값' },
      });
    });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
    cy.loginAndVisit('/admin/subscribers/import');
    cy.location('pathname').should('eq', '/admin/subscribers/import');
    cy.request('DELETE', '/api/import/subscribers');
    cy.clearLocalStorage();
    cy.visit('/admin/subscribers/import');
    cy.get('form.box').should('be.visible');
    cy.get('.list-selector input').type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    cy.get('.list-selector .tag').should('contain', listName);
  });

  it('decodes CP949 extension Hangul, reviews exclusions, imports arrays/strings, and remembers only the mapping', () => {
    cy.viewport(390, 844);
    cy.intercept('GET', '/api/subscribers?*list_id=*').as('keys');
    chooseFile('import-map-cp949.csv');
    cy.wait('@keys').then(({ request }) => {
      expect(request.query.per_page).to.eq('50');
      expect(request.query.order_by).to.eq('id');
    });
    cy.get('[data-cy=map-panel]').should('contain', 'CP949').and('contain', '똠 가상 하나');
    cy.get('.map-sheet-letters').should('contain', 'A').and('contain', 'F');
    cy.get('[data-cy=map-mode-raw] input').should('be.disabled');
    submit().should('be.disabled');
    setKey(4, 'member_code');
    ready(2);
    cy.get('.map-counts > div strong').then(($counts) => expect([...$counts].map((el) => el.textContent)).to.deep.eq(['2', '4', '1']));
    cy.get('.map-issues p strong').should('have.length', 4).each(($item) => expect($item.text()).to.include('1'));
    cy.get('.map-issues p > span').each(($item, index) => expect($item.text()).to.include(String([6, 5, 4, 7][index])));
    cy.get('[data-cy=map-column-error-0]').should('exist');
    cy.get('[data-cy=map-panel]').then(($panel) => {
      expect($panel.text()).not.to.include('csv-cp949-first@example.invalid');
      expect($panel.text()).not.to.include('wrong@localhost');
    });
    cy.get('.map-sheet tbody tr').should('have.length', 3);
    importRows(2);
    subscriber('csv-cp949-first@example.invalid').then((sub) => {
      expect(sub.name).to.eq('똠 가상 하나');
      expect(sub.attribs).to.deep.eq({ company_name: '가상 회사', tags: ['개발', 'AI', '마케팅'], member_code: '00123' });
    });
    cy.window().then((win) => {
      const stored = win.localStorage.getItem(storageKey);
      expect(JSON.parse(stored).columns['회원 번호']).to.deep.eq({ role: 'attribute', key: 'member_code' });
      ['example.invalid', '가상 하나', '가상 회사', '00123'].forEach((cell) => expect(stored).not.to.include(cell));
    });
    cy.get('.status button.is-primary').click();
    cy.get('form.box').should('be.visible');
    chooseFile('import-map-cp949.csv');
    cy.get('[data-cy=map-key-4]').should('have.value', 'member_code');
    ready(2);
    cy.window().then((win) => expect(win.document.documentElement.scrollWidth).to.eq(win.document.documentElement.clientWidth));
  });

  it('converts UTF-8 BOM to a comma CSV with the exact existing multipart API contract', () => {
    chooseFile('import-map-utf8-bom.csv');
    setKey(4, 'member_code');
    cy.intercept('POST', '/api/import/subscribers', (req) => {
      const body = typeof req.body === 'string' ? req.body : new TextDecoder().decode(req.body);
      expect(body).to.include('email,name,attributes\r\n');
      expect(body).to.include('"delim":","');
      expect(body).to.include(`"lists":[${listId}]`);
      expect(body).to.include('"overwrite_userinfo":false');
      expect(body).to.include('"overwrite_subscription_status":false');
      expect(body).not.to.include('메일 주소,성함');
      expect(body).to.include('""member_code"":""00123""');
    }).as('upload');
    importRows(2);
    cy.wait('@upload');
    subscriber('csv-utf8-first@example.invalid').its('attribs.tags').should('deep.eq', ['개발', 'AI']);
  });

  it('allows existing non-English/hyphen keys, and gracefully handles denied sample reads', () => {
    chooseFile('import-map-utf8-bom.csv');
    setKey(4, 'company-name');
    ready(2);
    cy.get('[data-cy=map-key-4]').clear();
    cy.get('[data-cy=map-key-4]').blur().focus();
    cy.contains('[data-cy=map-sheet] .dropdown-item', 'company-name').should('be.visible').click();
    cy.get('[data-cy=map-key-4]').should('have.value', 'company-name');
    cy.get('[data-cy=map-sheet]').scrollTo(880, 0, { ensureScrollable: false });
    cy.get('[data-cy=map-key-4]').focus().type('{selectall}new_key', { scrollBehavior: false });
    cy.get('[data-cy=map-key-4]').should('have.value', 'new_key');
    ready(2);
    setKey(4, 'company_name');
    cy.get('[data-cy=map-validation]').should('exist');
    submit().should('be.disabled');
    setKey(4, '회사명');
    ready(2);
    cy.intercept('GET', '/api/subscribers?*list_id=*', { statusCode: 403, body: { message: 'synthetic denied sample' } });
    cy.contains('.tags .tag', 'import-map-utf8-bom.csv').find('.delete').click();
    chooseFile('import-map-utf8-bom.csv');
    setKey(4, 'new_key');
    ready(2);
    cy.get('.toast').should('not.exist');
    importRows(2);
  });

  it('imports with a real import-only role despite subscriber sample reads returning 403', () => {
    const username = `csv-import-${listId}`;
    let roleId;
    cy.request('POST', '/api/roles/users', { name: username, permissions: ['subscribers:import'] }).then(({ body }) => { roleId = body.data.id; });
    cy.request('POST', '/api/roles/lists', { name: username, lists: [{ id: listId, permissions: ['list:get', 'list:manage'] }] }).then(({ body }) => {
      cy.request('POST', '/api/users', {
        username, password: 'csv-synthetic-only', password_login: true, email: `${username}@example.invalid`,
        name: '가상 가져오기 사용자', type: 'user', status: 'enabled', user_role_id: roleId, list_role_id: body.data.id,
      });
    });
    cy.visit('/admin/login?next=/admin/subscribers/import');
    cy.get('input[name=username]').type(username);
    cy.get('input[name=password]').type('csv-synthetic-only');
    cy.get('button[type=submit]').click();
    cy.get('form.box').should('be.visible');
    cy.get('.list-selector input').type(listName);
    cy.contains('.list-selector .autocomplete a', listName).click();
    cy.intercept('GET', '/api/subscribers?*list_id=*').as('deniedSample');
    inlineFile(`이메일,이름,추가 정보\nlimited-${listId}@example.invalid,가상 권한,00123`);
    cy.wait('@deniedSample').its('response.statusCode').should('eq', 403);
    setKey(2, 'custom_key');
    importRows(1);
    cy.loginAndVisit('/admin/subscribers/import');
    cy.location('pathname').should('eq', '/admin/subscribers/import');
    subscriber(`limited-${listId}@example.invalid`).its('attribs.custom_key').should('eq', '00123');
  });

  it('rejects truncated multibyte bytes, NUL and oversize input before upload', () => {
    chooseFile('import-map-broken.csv');
    cy.get('[data-cy=map-error]').should('be.visible');
    submit().should('be.disabled');
    inlineFile('email,name\na@example.invalid,\0');
    cy.get('[data-cy=map-error]').should('be.visible');
    submit().should('be.disabled');
    cy.window().then((win) => cy.spy(win.File.prototype, 'arrayBuffer').as('readFile'));
    inlineFile(Cypress.Buffer.alloc(10 * 1024 * 1024 + 1));
    cy.get('[data-cy=map-error]').should('contain', '10 MiB');
    cy.get('@readFile').should('not.have.been.called');
    submit().should('be.disabled');
    inlineFile(`email\n${'too-many@example.invalid\n'.repeat(50001)}`);
    cy.get('[data-cy=map-error]').should('contain', '50,000');
    submit().should('be.disabled');
  });

  it('lets users override encoding, change the delimiter, or treat the first row as data', () => {
    chooseFile('import-map-cp949.csv');
    cy.get('[data-cy=map-encoding]').select('utf-8');
    cy.get('[data-cy=map-error]').should('be.visible');
    cy.get('[data-cy=map-encoding]').select('cp949');
    cy.get('[data-cy=map-panel]').should('contain', '똠 가상 하나');
    inlineFile('이메일;이름;회사명\nsemi@example.invalid;가상 하나;00123');
    cy.get('input[name=delim]').should('have.value', ';');
    ready(1);
    cy.get('input[name=delim]').clear();
    submit().should('be.disabled');
    cy.get('input[name=delim]').type(';');
    ready(1);
    cy.window().then((win) => {
      const read = win.File.prototype.arrayBuffer;
      cy.stub(win.File.prototype, 'arrayBuffer').callsFake(function delayedRead() {
        return new Promise((resolve, reject) => setTimeout(() => read.call(this).then(resolve, reject), 200));
      });
    });
    inlineFile('noheader-a@example.invalid,가상 하나\nnoheader-b@example.invalid,가상 둘');
    cy.get('[data-cy=map-header]').click();
    cy.get('[data-cy=map-busy]').should('not.exist');
    cy.get('.map-sheet tbody tr').should('have.length', 2);
    cy.get('[data-cy=map-sheet]').scrollTo(220, 0, { ensureScrollable: false });
    cy.get('[data-cy=map-role-1]').select('name');
    ready(2);
    cy.get('.map-sheet tbody tr').first().find('th').should('have.text', '1');
    importRows(2);
    subscriber('noheader-a@example.invalid').its('name').should('eq', '가상 하나');
  });

  it('keeps standard UTF-8 CSV in original mode and preserves nested attributes', () => {
    inlineFile('email,name,attributes\nstandard@example.invalid,가상 표준,"{""nested"":{""code"":""00123""},""tags"":[""AI""]}"');
    cy.get('[data-cy=map-mode-raw] input').should('be.checked');
    submit().should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 30000 }).should('exist');
    subscriber('standard@example.invalid').its('attribs').should('deep.eq', { nested: { code: '00123' }, tags: ['AI'] });
  });

  it('preserves both overwrite defaults, replaces attributes only when chosen, and keeps blocklisting', () => {
    const email = `overwrite-${listId}@example.invalid`;
    cy.request('POST', '/api/subscribers', {
      email, name: '가상 원본', status: 'enabled', lists: [listId], preconfirm_subscriptions: true,
      attribs: { keep: 'original', legacy: 'value' },
    });
    const upload = (confirm = false) => {
      inlineFile(`email,name,attributes\n${email},가상 변경,"{""new_key"":""00123""}"`);
      cy.get('[data-cy=map-mode-raw] input').should('be.checked');
      submit().should('not.be.disabled').click();
      if (confirm) cy.get('.modal button.is-primary').click();
      cy.get('.status .has-text-success', { timeout: 30000 }).should('exist');
    };
    upload();
    subscriber(email).then((sub) => {
      expect(sub.name).to.eq('가상 원본');
      expect(sub.attribs).to.deep.eq({ keep: 'original', legacy: 'value' });
      expect(sub.lists.find((list) => list.id === listId).subscription_status).to.eq('confirmed');
    });
    cy.get('.status button.is-primary').click();
    cy.get('[data-cy=overwrite-user-info]').click();
    upload();
    subscriber(email).then((sub) => {
      expect(sub.name).to.eq('가상 변경');
      expect(sub.attribs).to.deep.eq({ new_key: '00123' });
      expect(sub.lists.find((list) => list.id === listId).subscription_status).to.eq('confirmed');
    });
    cy.get('.status button.is-primary').click();
    cy.get('[data-cy=overwrite-sub-status]').click();
    upload(true);
    subscriber(email).then((sub) => expect(sub.lists.find((list) => list.id === listId).subscription_status).to.eq('unconfirmed'));
    cy.get('.status button.is-primary').click();
    cy.get('[data-cy=check-blocklist] .check').click();
    inlineFile(`email\n${email}`);
    cy.get('[data-cy=map-mode-raw] input').should('be.checked');
    submit().should('not.be.disabled').click();
    cy.get('.status .has-text-success', { timeout: 30000 }).should('exist');
    subscriber(email).its('status').should('eq', 'blocklisted');
  });

  it('terminates the Worker on file removal and does not leave an uploadable result', () => {
    cy.window().then((win) => cy.spy(win.Worker.prototype, 'terminate').as('terminateWorker'));
    chooseFile('import-map-cp949.csv');
    cy.get('.map-sheet').should('exist');
    cy.contains('.tags .tag', 'import-map-cp949.csv').find('.delete').click();
    cy.get('@terminateWorker').should('have.been.called');
    cy.get('[data-cy=map-panel]').should('not.exist');
    submit().should('be.disabled');
  });

  it('warns when finished has fewer imported rows and checks status after a failed upload', () => {
    inlineFile('이메일,이름\nmismatch-a@example.invalid,가상 하나\nmismatch-b@example.invalid,가상 둘');
    ready(2);
    cy.intercept('POST', '/api/import/subscribers', { statusCode: 500, body: { message: 'synthetic upload failure' } }).as('failedUpload');
    cy.intercept('GET', '/api/import/subscribers', { body: { data: { status: 'finished', imported: 1, total: 2 } } }).as('statusAfterFailure');
    submit().click();
    cy.wait('@failedUpload');
    cy.wait('@statusAfterFailure');
    cy.get('[data-cy=map-mismatch]').should('be.visible');
    cy.get('.status .has-text-success').should('not.exist');
  });

  it('keeps the UI and preview bounded for 50,000 rows, then imports them all', () => {
    const text = `이메일,이름,회사명\n${Array.from({ length: 50000 }, (_, i) => `bulk-${i}@example.invalid,가상 ${i},00123`).join('\n')}`;
    inlineFile(text);
    ready(50000);
    cy.get('.map-sheet tbody tr').should('have.length', 3);
    cy.get('[data-cy=map-preview] tbody tr').should('have.length', 5);
    importRows(50000);
    subscriber('bulk-49999@example.invalid').its('attribs.company_name').should('eq', '00123');
  });
  it('still sends an explicitly chosen original CSV and reports the server header failure', () => {
    cy.get('input[type=file]').selectFile({ contents: Cypress.Buffer.from('email,name\nlegacy-failure@example.invalid,가상 구분자'), fileName: 'synthetic.csv', mimeType: 'text/csv' }, { force: true });
    cy.get('[data-cy=map-mode-raw] input').should('be.checked');
    cy.get('[data-cy=map-mode-raw]').click();
    cy.get('input[name=delim]').clear().type('|');
    cy.get('[data-cy=map-busy]').should('not.exist');
    cy.get('button[type=submit]').should('not.be.disabled').click();
    cy.get('.status .has-text-danger', { timeout: 15000 }).should('contain', 'failed');
    cy.request('/api/import/subscribers').its('body.data.status').should('eq', 'failed');
  });

});
