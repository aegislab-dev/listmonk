// Disposable synthetic-data coverage for the column presets of the subscribers table (LST-17):
// the built-in B2B preset, presets people make, the list form's choice, taking over a base
// list's preset, deleting a preset, the 1440px layout and who may change presets.
const run = Date.now();
const pass = 'member-pass-1234';
const memberUser = `presetmember${run}`;
const salesName = `영업 시험 ${run}`;
const doomedName = `삭제 시험 ${run}`;
const wideName = `여덟열 시험 ${run}`;

let roleId;
let memberId;
let salesId; // "u<n>"
let listId; // a list on the sales preset
let baseId; // a list the dynamic list takes over from

const loginAs = (user, password, next) => {
  cy.visit(`/admin/login?next=${next}`);
  cy.get('input[name=username]').invoke('val', user);
  cy.get('input[name=password]').invoke('val', password);
  cy.get('button').click();
};
const api = (method, url, body) => cy.request({
  method, url, body, failOnStatusCode: false,
});
const presets = () => cy.request('/api/lists/presets').then(({ body }) => body.data);
const subscriber = (suffix, attribs, lists) => cy.request('POST', '/api/subscribers', {
  email: `presets-${run}-${suffix}@example.invalid`, name: `가상 ${suffix}`, status: 'enabled', attribs, lists,
});
// Retries until the header row has these attribute columns (the list loads after the table).
const expectHeads = (want) => cy.get('thead th.attrib-head').should(($th) => {
  expect([...$th].map((th) => th.innerText.trim())).to.deep.eq(want);
});
const wrapper = () => cy.get('.subscribers-table .table-wrapper');

describe('column presets', () => {
  before(() => {
    cy.loginAndVisit('/admin/lists/presets');
    cy.request('POST', '/api/roles/users', {
      name: `프리셋 시험 ${run}`, permissions: ['subscribers:get', 'campaigns:get'],
    }).then(({ body }) => {
      roleId = body.data.id;
      return cy.request('POST', '/api/users', {
        username: memberUser, email: `${memberUser}@example.invalid`, name: `팀원 ${run}`, type: 'user', status: 'enabled',
        password_login: true, password: pass, password2: pass, user_role_id: roleId,
      });
    }).then(({ body }) => { memberId = body.data.id; });
  });

  beforeEach(() => {
    cy.viewport(1440, 1000);
  });

  after(() => {
    cy.loginAndVisit('/admin/lists/presets');
    cy.request('/api/lists?per_page=all').then(({ body }) => {
      body.data.results.filter((l) => l.name.endsWith(`${run}`)).forEach((l) => cy.request('DELETE', `/api/lists/${l.id}`));
    });
    cy.request(`/api/subscribers?per_page=all&query=${encodeURIComponent(`subscribers.email LIKE 'presets-${run}%'`)}`).then(({ body }) => {
      body.data.results.forEach((s) => cy.request('DELETE', `/api/subscribers/${s.id}`));
    });
    presets().then((all) => {
      all.filter((p) => !p.builtin && [salesName, doomedName, wideName, `${salesName} 복사본`].includes(p.name))
        .forEach((p) => cy.request('DELETE', `/api/lists/presets/${p.id.slice(1)}`));
    });
    cy.request('DELETE', `/api/users/${memberId}`);
    cy.request('DELETE', `/api/roles/${roleId}`);
  });

  it('has the two built-in presets and checks what people make', () => {
    cy.request('/api/config').its('body.data.axm_column_presets').should((all) => {
      expect(all.slice(0, 2).map((p) => p.id)).to.deep.eq(['creator', 'b2b']);
      expect(all[1].columns).to.deep.eq(['company', 'brand', 'title', 'industry', 'mail_status', 'owner']);
      expect(all[1].builtin).to.eq(true);
    });
    // Rejected: empty and repeated names, a built-in name, no columns, nine, a repeat, a blank key.
    const bad = (body) => api('POST', '/api/lists/presets', body).its('status').should('eq', 400);
    bad({ name: ' ', columns: ['a'] });
    bad({ name: 'B2B 세일즈', columns: ['a'] });
    bad({ name: 'creator', columns: ['a'] });
    bad({ name: 'x', columns: [] });
    bad({ name: 'x', columns: ['1', '2', '3', '4', '5', '6', '7', '8', '9'] });
    bad({ name: 'x', columns: ['a', 'b', 'a'] });
    bad({ name: 'x', columns: ['a', ' '] });
    api('POST', '/api/lists/presets', { name: salesName, columns: ['company', 'brand', 'mrr'] }).then(({ status, body }) => {
      expect(status).to.eq(200);
      salesId = body.data.id;
      expect(salesId).to.match(/^u\d+$/);
      expect(body.data.columns).to.deep.eq(['company', 'brand', 'mrr']);
    });
    // The same name in another letter case is the same name.
    api('POST', '/api/lists/presets', { name: salesName.toUpperCase(), columns: ['a'] }).its('status').should('eq', 400);
    // Built-in presets are not rows: they cannot be edited or deleted.
    api('PUT', '/api/lists/presets/creator', { name: 'x', columns: ['a'] }).its('status').should('eq', 400);
    api('DELETE', '/api/lists/presets/b2b').its('status').should('eq', 400);
    // A list cannot take a preset that does not exist.
    api('POST', '/api/lists', { name: `Presets bad ${run}`, axm_preset: 'u999999' }).its('status').should('eq', 400);
  });

  it('makes, reorders and copies a preset on the screen', () => {
    cy.visit('/admin/lists/presets');
    cy.get('[data-cy=preset-builtin]').should('have.length', 2);
    cy.contains('tbody tr', 'B2B').find('[data-cy=btn-edit]').should('not.exist');
    cy.contains('tbody tr', 'B2B').find('[data-cy=btn-delete]').should('not.exist');
    cy.contains('tbody tr', 'B2B').find('[data-cy=btn-clone]').should('exist');

    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=preset-form]').should('be.visible');
    cy.get('[data-cy=preset-name-input]').type(doomedName);
    ['company', 'brand', 'my_own_key'].forEach((key) => {
      cy.get('[data-cy=preset-add-input]').type(`${key}{enter}`);
    });
    cy.get('[data-cy=preset-edit-item]').should('have.length', 3);
    // A key twice is refused, the order is changed by the arrows, and a key is taken out.
    cy.get('[data-cy=preset-add-input]').type('company{enter}');
    cy.get('[data-cy=preset-edit-item]').should('have.length', 3);
    cy.get('[data-cy=preset-edit-item]').eq(2).find('[data-cy=preset-up]').click();
    cy.get('[data-cy=preset-edit-item] .preset-key').should(($k) => {
      expect([...$k].map((k) => k.innerText.trim())).to.deep.eq(['company', 'my_own_key', 'brand']);
    });
    cy.get('[data-cy=preset-edit-item]').eq(0).find('[data-cy=preset-up]').should('be.disabled');
    cy.get('[data-cy=preset-edit-item]').eq(2).find('[data-cy=preset-down]').should('be.disabled');
    cy.get('[data-cy=preset-column-count]').should('contain.text', '3/8');
    cy.get('[data-cy=btn-save]').click();
    cy.contains('tbody tr', doomedName).find('[data-cy=preset-count]').should('contain.text', '0');
    presets().then((all) => {
      const made = all.find((p) => p.name === doomedName);
      expect(made.columns).to.deep.eq(['company', 'my_own_key', 'brand']);
    });

    // A name that exists is refused on the form.
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=preset-name-input]').type(salesName.toLowerCase());
    cy.get('[data-cy=preset-add-input]').type('company{enter}');
    cy.get('[data-cy=btn-save]').click();
    cy.get('[data-cy=preset-form]').should('contain.text', 'already exists');
    cy.get('[data-cy=preset-form] [data-cy=btn-save]').parents('.modal-card').find('button').contains('Close').click();

    // Copy a built-in preset: the copy starts with its columns and is a new preset.
    cy.contains('tbody tr', 'B2B').find('[data-cy=btn-clone]').click();
    cy.get('[data-cy=preset-edit-item]').should('have.length', 6);
    cy.get('[data-cy=preset-name-input]').clear().type(`${salesName} 복사본`);
    cy.get('[data-cy=btn-save]').click();
    cy.contains('tbody tr', `${salesName} 복사본`).find('[data-cy=preset-columns] li').should('have.length', 6);
  });

  it('is chosen on the list form and shapes the subscribers table', () => {
    api('POST', '/api/lists', { name: `Presets list ${run}`, type: 'private', optin: 'single', tags: [] }).then(({ body }) => {
      listId = body.data.id;
      subscriber('a', {
        company: 'Alpha Foods', brand: 'Alpha Kitchen', mrr: 1000,
      }, [listId]);
      subscriber('b', {
        company: 'Beta Beauty', brand: 'Beta Glow', mrr: 2000,
      }, [listId]);
    }).then(() => {
      cy.visit(`/admin/lists/${listId}`);
      cy.get('[data-cy=list-preset]').should('have.value', 'creator');
      cy.get('[data-cy=list-preset]').select(salesId);
      cy.get('[data-cy=list-preset-manage]').should('exist');
      cy.get('[data-cy=btn-save]').click();
      // The form closes when the server has saved the list.
      cy.get('[data-cy=list-preset]').should('not.exist');
      cy.request(`/api/lists/${listId}`).its('body.data.axm_preset').should('eq', salesId);

      cy.visit(`/admin/subscribers/lists/${listId}`);
      cy.get('[data-cy=count]').should('have.text', '2');
      expectHeads(['company', 'brand', 'mrr']);
      cy.get('[data-cy=column-preset] strong').should('have.text', salesName);
      cy.get('tbody td.attrib-cell[data-label=brand]').should(($td) => {
        expect([...$td].map((td) => td.innerText.trim())).to.deep.eq(['Beta Glow', 'Alpha Kitchen']);
      });

      // Reordering the preset changes the table of every list that uses it.
      cy.request('PUT', `/api/lists/presets/${salesId.slice(1)}`, { name: salesName, columns: ['mrr', 'company', 'brand'] });
      cy.reload();
      expectHeads(['mrr', 'company', 'brand']);
      cy.request('PUT', `/api/lists/presets/${salesId.slice(1)}`, { name: salesName, columns: ['company', 'brand', 'mrr'] });
    });
  });

  it('takes over the preset of the base list for a new dynamic list', () => {
    api('POST', '/api/lists', {
      name: `Presets base ${run}`, type: 'private', optin: 'single', tags: [], axm_preset: salesId,
    }).then(({ body }) => { baseId = body.data.id; }).then(() => {
      cy.visit('/admin/lists');
      cy.get('[data-cy=btn-new]').click();
      cy.get('[data-cy=list-composition]').select('dynamic');
      cy.get('[data-cy=list-preset]').should('have.value', 'creator');
      cy.get('[data-cy=dynamic-base]').select('lists');
      cy.get('[data-cy=dynamic-base-lists] input').first().type(`Presets base ${run}`);
      cy.get('[data-cy=dynamic-base-lists] .dropdown-item').first().click();
      cy.get('[data-cy=list-preset]').should('have.value', salesId);
      cy.get('[data-cy=list-preset]').closest('.field').should('contain.text', `Presets base ${run}`);
      // The server does the same when no preset is sent.
      cy.request('POST', '/api/lists', {
        name: `Presets dynamic ${run}`,
        axm_filter: {
          version: 1, base: { type: 'lists', lists: [baseId] }, match: 'all', conditions: [{ key: 'company', op: 'eq', value: 'Alpha Foods' }],
        },
      }).its('body.data.axm_preset').should('eq', salesId);
    });
  });

  it('fits the six B2B columns in 1440px and keeps the buttons when eight columns scroll', () => {
    let b2bList;
    let wideList;
    api('POST', '/api/lists', {
      name: `Presets b2b ${run}`, type: 'private', optin: 'single', tags: [], axm_preset: 'b2b',
    }).then(({ body }) => {
      b2bList = body.data.id;
      for (let i = 1; i <= 3; i += 1) {
        subscriber(`b2b${i}`, {
          company: `Han Foods ${i}`, brand: `Han Kitchen ${i}`, title: 'Marketing Lead', industry: 'Food', mail_status: 'Sent', owner: 'Kim',
        }, [b2bList]);
      }
    }).then(() => {
      cy.visit(`/admin/subscribers/lists/${b2bList}`);
      expectHeads(['company', 'brand', 'title', 'industry', 'mail_status', 'owner']);
      // The date columns give way to the attribute columns.
      cy.get('thead th.cy-updated_at').should('not.exist');
      cy.get('thead th.cy-created_at').should('not.exist');
      cy.get('[data-cy=table-wide-hint]').should('not.exist');
      wrapper().should(($w) => expect($w[0].scrollWidth).to.be.at.most($w[0].clientWidth + 1));
      cy.get('td.actions').first().invoke('css', 'position').should('eq', 'static');
    });

    // Eight columns with longer values scroll inside the table; the row buttons stay.
    api('POST', '/api/lists/presets', {
      name: wideName, columns: ['company', 'brand', 'title', 'industry', 'mail_status', 'owner', 'website', 'country'],
    }).then(({ body }) => api('POST', '/api/lists', {
      name: `Presets wide ${run}`, type: 'private', optin: 'single', tags: [], axm_preset: body.data.id,
    })).then(({ body: l }) => {
      wideList = l.data.id;
      for (let i = 1; i <= 3; i += 1) {
        subscriber(`wide${i}`, {
          company: 'Daon Holdings Korea Co., Ltd', brand: 'Premium Natural Skin Care', title: 'Senior Director of Partnerships',
          industry: '건강기능식품 및 건강보조식품', mail_status: '발송 완료(회신 대기)', owner: '김영업 (서울 영업 1팀)',
          website: 'https://www.example.invalid/brand', country: 'KR',
        }, [wideList]);
      }
    }).then(() => {
      cy.visit(`/admin/subscribers/lists/${wideList}`);
      cy.get('thead th.attrib-head').should('have.length', 8);
      cy.get('[data-cy=table-wide-hint]').should('be.visible');
      wrapper().should(($w) => expect($w[0].scrollWidth).to.be.greaterThan($w[0].clientWidth));
      cy.get('td.actions').first().invoke('css', 'position').should('eq', 'sticky');
      cy.get('td.actions').first().then(($td) => {
        const box = $td[0].getBoundingClientRect();
        const wrap = $td.closest('.table-wrapper')[0].getBoundingClientRect();
        expect(Math.round(box.right)).to.be.at.most(Math.round(wrap.right) + 1);
        expect(box.left).to.be.greaterThan(wrap.left);
      });
      cy.window().then((win) => expect(win.document.documentElement.scrollWidth).to.be.at.most(win.innerWidth));
    });
  });

  it('shows what changes before a preset is deleted, then moves its lists to the default one', () => {
    let doomedList;
    presets().then((all) => api('POST', '/api/lists', {
      name: `Presets doomed ${run}`, type: 'private', optin: 'single', tags: [], axm_preset: all.find((p) => p.name === doomedName).id,
    })).then(({ body }) => { doomedList = body.data.id; }).then(() => {
      cy.visit('/admin/lists/presets');
      cy.contains('tbody tr', doomedName).find('[data-cy=preset-count]').should('contain.text', '1');
      cy.contains('tbody tr', doomedName).find('[data-cy=btn-delete]').click();
      cy.get('[data-cy=warn-box]').should('contain.text', `Presets doomed ${run}`).and('contain.text', 'Creator');
      // Cancel keeps everything.
      cy.get('.dialog .button').contains('Cancel').click();
      presets().then((all) => expect(all.some((p) => p.name === doomedName)).to.eq(true));
      cy.contains('tbody tr', doomedName).find('[data-cy=btn-delete]').click();
      cy.get('.dialog .button.is-danger').click();
      cy.contains('tbody tr', doomedName).should('not.exist');
      cy.request(`/api/lists/${doomedList}`).its('body.data.axm_preset').should('eq', 'creator');
      // Other lists are not touched.
      cy.request(`/api/lists/${listId}`).its('body.data.axm_preset').should('eq', salesId);
      cy.request('/api/config').its('body.data.axm_column_presets').should((all) => {
        expect(all.some((p) => p.name === doomedName)).to.eq(false);
      });
    });
  });

  it('fits the narrow screen', () => {
    cy.viewport(390, 844);
    cy.visit('/admin/lists/presets');
    cy.get('[data-cy=preset-builtin]').should('have.length', 2);
    cy.window().then((win) => {
      expect(win.document.documentElement.scrollWidth).to.be.at.most(win.innerWidth);
    });
    cy.get('[data-cy=btn-new]').click();
    cy.get('[data-cy=preset-form]').should('be.visible');
    cy.window().then((win) => {
      expect(win.document.documentElement.scrollWidth).to.be.at.most(win.innerWidth);
    });
  });

  it('lets only who manages all lists change presets, and shows no list names to the others', () => {
    cy.then(() => loginAs(memberUser, pass, '/admin/lists/presets'));
    cy.get('[data-cy=preset-builtin]').should('have.length', 2);
    cy.get('[data-cy=presets-readonly]').should('be.visible');
    cy.get('[data-cy=btn-new]').should('not.exist');
    cy.get('[data-cy=btn-edit]').should('not.exist');
    cy.get('[data-cy=btn-delete]').should('not.exist');
    cy.get('[data-cy=btn-clone]').should('not.exist');
    cy.then(() => {
      api('GET', '/api/lists/presets').then(({ status, body }) => {
        expect(status).to.eq(200);
        expect(body.data.every((p) => !p.lists)).to.eq(true);
        expect(body.data.find((p) => p.id === salesId).list_count).to.be.greaterThan(0);
      });
      api('POST', '/api/lists/presets', { name: `x ${run}`, columns: ['a'] }).its('status').should('eq', 403);
      api('PUT', `/api/lists/presets/${salesId.slice(1)}`, { name: 'x', columns: ['a'] }).its('status').should('eq', 403);
      api('DELETE', `/api/lists/presets/${salesId.slice(1)}`).its('status').should('eq', 403);
    });
    cy.loginAndVisit('/admin/lists/presets');
    presets().then((all) => expect(all.find((p) => p.id === salesId).name).to.eq(salesName));
  });
});
