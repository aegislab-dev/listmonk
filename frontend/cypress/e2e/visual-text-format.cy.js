// LST-77: bold, underline and font size on part of a visual Text block. The toolbar
// formats what is selected in the Content box, the canvas shows it, the saved text
// keeps it as inline HTML, and a real test mail (Mailpit) carries it next to the
// personalization chip (LST-3) and the automatic open/click tracking (LST-9).
const stamp = Date.now();
const subEmail = `format-${stamp}@example.invalid`;
const text = '안녕하세요, {{ .Subscriber.Name }}님.\n12월 3일까지 신청하면 수수료가 0원입니다. 놓치지 마세요.';
const doc = {
  root: { type: 'EmailLayout', data: { childrenIds: ['h', 't', 'b'] } },
  h: { type: 'Heading', data: { props: { text: '12월 혜택 안내', level: 'h2' }, style: {} } },
  t: { type: 'Text', data: { props: { text }, style: { padding: { top: 8, bottom: 8, left: 24, right: 24 } } } },
  b: { type: 'Button', data: { props: { text: '자세히 보기', url: `https://example.test/promo-${stamp}` }, style: {} } },
};
const line1 = '<span style="font-size:24px"><strong><u>안녕하세요, {{ .Subscriber.Name }}님.</u></strong></span>';
const stored = `<div class="axm-text">${line1}<br><strong>12월 3일까지</strong> 신청하면 `
  + '<span style="font-size:20px"><strong><u>수수료가 0원입니다</u></strong></span>. 놓치지 마세요.</div>';
const formatted = '<span style="font-size:20px"><strong><u>수수료가 0원입니다</u></strong></span>';

let campaignId;
let listId;

const editorDoc = () => cy.get('#visual-editor').its('0.contentDocument');
const ready = () => cy.waitUntil(() => cy.get('#visual-editor').then(($f) => Boolean($f[0].contentWindow.EmailBuilder
  && $f[0].contentWindow.EmailBuilder.isRendered('visual-editor-container')
)), { timeout: 20000 });
const findBlock = (d, start) => [...d.querySelectorAll('#visual-editor-container div')].reverse()
  .find((el) => el.textContent.trim().startsWith(start));
const openText = (reload = false, id = null, start = '안녕하세요') => {
  // A visit that only changes the hash does not load the page again.
  if (reload) cy.reload();
  else cy.visit(`/admin/campaigns/${id || campaignId}#content`);
  ready();
  // The document is loaded a moment after the editor renders.
  cy.waitUntil(() => editorDoc().then((d) => Boolean(findBlock(d, start))), { timeout: 10000 });
  editorDoc().then((d) => findBlock(d, start).click());
  editorDoc().find('[role=toolbar] [data-format=bold]').should('exist');
};
const box = () => editorDoc().find('[role=textbox][contenteditable=true]');
const select = (phrase) => box().then(($b) => {
  const el = $b[0];
  const d = el.ownerDocument;
  const walker = d.createTreeWalker(el, 4);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const i = n.textContent.indexOf(phrase);
    if (i >= 0) {
      el.focus();
      const r = d.createRange();
      r.setStart(n, i);
      r.setEnd(n, i + phrase.length);
      d.getSelection().removeAllRanges();
      d.getSelection().addRange(r);
      return;
    }
  }
  throw new Error(`no "${phrase}" in the Content box`);
});
// From the start of the box to the end of "님." after the name chip: the chip is inside.
const selectFirstLine = () => box().then(($b) => {
  const el = $b[0];
  const chip = el.querySelector('[data-token]');
  el.focus();
  const r = el.ownerDocument.createRange();
  r.setStart(el.firstChild, 0);
  r.setEnd(chip.nextSibling, 2);
  el.ownerDocument.getSelection().removeAllRanges();
  el.ownerDocument.getSelection().addRange(r);
});
const pickSize = (px) => {
  editorDoc().find('[data-format=size]').parent().click();
  editorDoc().find('li[role=option]').contains(px).click();
};
const canvasText = () => editorDoc().then((d) => [...d.querySelectorAll('#visual-editor-container div')].reverse()
  .find((el) => el.textContent.startsWith('안녕하세요')));

describe('Visual editor inline formatting', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', {
      name: `Format ${stamp}`, type: 'private', optin: 'single', no_team: true,
    }).then(({ body: list }) => {
      listId = list.data.id;
      cy.request('POST', '/api/subscribers', {
        name: '김시험', email: subEmail, status: 'enabled', lists: [list.data.id],
      });
      cy.request('POST', '/api/campaigns', {
        name: `Format ${stamp}`,
        subject: `Format subject ${stamp}`,
        lists: [list.data.id],
        type: 'regular',
        content_type: 'visual',
        messenger: 'email',
        body: '<p>x</p>',
        body_source: JSON.stringify(doc),
      }).then(({ body: c }) => { campaignId = c.data.id; });
    });
  });

  beforeEach(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.get('.menu').should('exist');
  });

  it('formats part of a Text block and keeps it after saving and reopening', () => {
    openText();
    // A plain block: the Markdown switch is there until the first formatting.
    editorDoc().find('label:contains("Markdown")').should('have.length', 1);

    // A whole line with the name chip in it: the chip gets the formatting too.
    selectFirstLine();
    editorDoc().find('[data-format=bold]').click();
    editorDoc().find('[data-format=underline]').click();
    pickSize('24px');
    select('수수료가 0원입니다');
    editorDoc().find('[data-format=bold]').click();
    editorDoc().find('[data-format=underline]').click();
    pickSize('20px');
    // Ctrl+B does the same as the button.
    select('12월 3일까지');
    box().type('{ctrl}b');

    canvasText().then(($el) => {
      const el = $el[0];
      const span = [...el.querySelectorAll('span')].find((s) => s.style.fontSize === '20px');
      expect(span, 'canvas 20px span').to.exist;
      expect(span.querySelector('strong u').textContent).to.equal('수수료가 0원입니다');
      expect([...el.querySelectorAll('strong')].map((s) => s.textContent)).to.include('12월 3일까지');
      // The personalization chip stays a chip, not a raw tag (LST-3).
      expect(el.textContent).to.not.contain('{{');
      const chip = [...el.querySelectorAll('span')].find((s) => s.style.backgroundColor === 'rgb(232, 242, 255)');
      expect(chip, 'chip').to.exist;
      expect(chip.closest('u') && chip.closest('strong') && chip.closest('span[style*="24px"]'), 'chip formatted').to.exist;
    });
    // The block now holds formatted HTML; Markdown would print the tags, so it is gone.
    editorDoc().find('label:contains("Markdown")').should('not.exist');
    // Italic is not something this box stores: Ctrl+I leaves no trace.
    select('놓치지');
    box().type('{ctrl}i');
    box().find('i, em').should('not.exist');

    cy.get('button[data-cy=btn-save]').click();
    cy.waitUntil(() => cy.request(`/api/campaigns/${campaignId}`)
      .then(({ body }) => JSON.parse(body.data.body_source).t.data.props.text === stored), { timeout: 10000 });
    cy.request(`/api/campaigns/${campaignId}`).then(({ body }) => {
      expect(JSON.parse(body.data.body_source).t.data.props.markdown).to.equal(true);
      expect(body.data.body).to.contain(formatted).and.contain('<strong>12월 3일까지</strong>')
        .and.contain(`${line1}<br/>`);
    });

    // Reopened, the box shows the same formatting and the chip.
    openText(true);
    box().find('[data-token="{{ .Subscriber.Name }}"]').should('exist');
    box().find('span[data-axm-size="20"]').should('have.text', '수수료가 0원입니다')
      .find('strong u').should('have.text', '수수료가 0원입니다');
    select('수수료가');
    editorDoc().find('[data-format=bold]').should('have.attr', 'aria-pressed', 'true');
    editorDoc().find('[data-format=size]').parent().should('contain', '20px');

    // Clear formatting takes all three off the selection.
    select('수수료가 0원입니다');
    editorDoc().find('[data-format=clear]').click();
    canvasText().then(($el) => {
      const el = $el[0];
      const has = (sel) => [...el.querySelectorAll(sel)].some((s) => s.textContent.includes('수수료'));
      expect(has('u') || has('strong') || has('span[style*="20px"]'), 'formatting left on 수수료').to.equal(false);
      expect([...el.querySelectorAll('strong')].map((s) => s.textContent)).to.include('12월 3일까지');
    });
  });

  it('sends the formatting with personalization and tracking', () => {
    // The campaign as saved by the test above.
    cy.request(`/api/campaigns/${campaignId}`).its('body.data.body').should('contain', formatted);
    cy.visit(`/admin/campaigns/${campaignId}`);
    cy.get('[data-cy=test-send] input[data-cy=test-emails]').type(`${subEmail}{enter}`);
    cy.intercept('POST', `/api/campaigns/${campaignId}/test`).as('testSend');
    cy.get('[data-cy=test-send] [data-cy=btn-test-send]').click();
    cy.wait('@testSend').its('response.statusCode').should('eq', 200);

    const inbox = Cypress.env('mailpitUrl');
    expect(inbox, 'CYPRESS_mailpitUrl (the test inbox)').to.be.a('string').and.not.be.empty;
    const received = (tries = 30) => cy.request(`${inbox}/api/v1/search?query=${encodeURIComponent(`subject:"Format subject ${stamp}"`)}`)
      .then(({ body }) => {
        if (!body.messages.length && tries > 0) {
          cy.wait(1000);
          return received(tries - 1);
        }
        return body.messages[0];
      });
    received().then((m) => cy.request(`${inbox}/api/v1/message/${m.ID}`)).then(({ body }) => {
      const html = body.HTML;
      expect(html).to.contain(formatted);
      expect(html).to.contain('<span style="font-size:24px"><strong><u>안녕하세요, 김시험님.</u></strong></span><br/><strong>12월 3일까지</strong>');
      // LST-9: the button link is tracked and the view pixel is added.
      expect(html).to.not.contain(`href="https://example.test/promo-${stamp}"`);
      expect(html).to.match(/href="[^"]*\/link\/[^"]+"/);
      expect(html).to.match(/\/campaign\/[^"]+\/px\.png/);
    });
  });

  it('types in the chosen size right after picking it with nothing selected', () => {
    openText();
    select('놓치지').then(() => box().then(($b) => $b[0].ownerDocument.getSelection().collapseToEnd()));
    pickSize('28px');
    // The menu gives the caret back to the box, where it was.
    editorDoc().its('activeElement').should('have.attr', 'role', 'textbox');
    editorDoc().then((d) => d.execCommand('insertText', false, 'ABC'));
    box().find('span[data-axm-size="28"]').should('contain', 'ABC');
    cy.get('button[data-cy=btn-save]').click();
    cy.waitUntil(() => cy.request(`/api/campaigns/${campaignId}`)
      .then(({ body }) => JSON.parse(body.data.body_source).t.data.props.text.includes('놓치지<span style="font-size:28px">ABC</span> 마세요')),
    { timeout: 10000 });
  });

  it('keeps a Markdown list a list when its lines are made bold', () => {
    const md = { root: { type: 'EmailLayout', data: { childrenIds: ['t'] } },
      t: { type: 'Text', data: { props: { text: '- 여름 상품\n- 겨울 상품', markdown: true }, style: {} } } };
    cy.request('POST', '/api/campaigns', {
      name: `Format list ${stamp}`, subject: 'list', lists: [listId], type: 'regular', content_type: 'visual', messenger: 'email',
      body: '<p>x</p>', body_source: JSON.stringify(md),
    }).then(({ body: c }) => {
      openText(false, c.data.id, '여름');
      editorDoc().find('#visual-editor-container li').should('have.length', 2);
      box().then(($b) => {
        const el = $b[0];
        el.focus();
        const r = el.ownerDocument.createRange();
        r.selectNodeContents(el);
        el.ownerDocument.getSelection().removeAllRanges();
        el.ownerDocument.getSelection().addRange(r);
      });
      editorDoc().find('[data-format=bold]').click();
      editorDoc().find('#visual-editor-container li strong').should('have.length', 2);
      editorDoc().find('#visual-editor-container li').should('have.length', 2);
      cy.get('button[data-cy=btn-save]').click();
      cy.waitUntil(() => cy.request(`/api/campaigns/${c.data.id}`).then(({ body }) => JSON.parse(body.data.body_source).t.data.props.text
        === '- <strong>여름 상품</strong>\n- <strong>겨울 상품</strong>'), { timeout: 10000 });
    });
  });

  it('undoes and redoes formatting on a line with the name chip without losing text', () => {
    const greet = '안녕하세요 {{ .Subscriber.Name }}님';
    const md = { root: { type: 'EmailLayout', data: { childrenIds: ['t'] } }, t: { type: 'Text', data: { props: { text: greet }, style: {} } } };
    cy.request('POST', '/api/campaigns', {
      name: `Format undo ${stamp}`, subject: 'undo', lists: [listId], type: 'regular', content_type: 'visual', messenger: 'email',
      body: '<p>x</p>', body_source: JSON.stringify(md),
    }).then(({ body: c }) => {
      const stored = () => cy.request(`/api/campaigns/${c.data.id}`).then(({ body }) => JSON.parse(body.data.body_source).t.data.props);
      const save = () => { cy.get('button[data-cy=btn-save]').click(); cy.wait(800); };
      openText(false, c.data.id, '안녕하세요');
      box().then(($b) => {
        const el = $b[0];
        el.focus();
        const r = el.ownerDocument.createRange();
        r.selectNodeContents(el);
        el.ownerDocument.getSelection().removeAllRanges();
        el.ownerDocument.getSelection().addRange(r);
      });
      editorDoc().find('[data-format=bold]').click();
      box().find('strong [data-token]').should('exist');
      box().type('{ctrl}z');
      box().find('b, strong').should('not.exist');
      box().should('contain.text', '안녕하세요').find('[data-token]').should('exist');
      save();
      stored().should('deep.include', { text: greet, markdown: false });
      box().type('{ctrl}y');
      box().find('strong [data-token]').should('exist');
      save();
      stored().its('text').should('eq', `<div class="axm-text"><strong>${greet}</strong></div>`);
    });
  });
});
