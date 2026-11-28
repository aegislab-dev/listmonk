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
const textCampaign = (subject, content, markdown = true) => cy.request('POST', '/api/campaigns', {
  name: subject, subject, lists: [listId], type: 'regular', content_type: 'visual', messenger: 'email',
  body: '<p>x</p>',
  body_source: JSON.stringify({
    root: { type: 'EmailLayout', data: { childrenIds: ['t'] } },
    t: { type: 'Text', data: { props: { text: content, markdown }, style: {} } },
  }),
}).its('body.data.id');
const saveText = (id, expected) => {
  cy.get('button[data-cy=btn-save]').click();
  return cy.waitUntil(() => cy.request(`/api/campaigns/${id}`).then(({ body }) => {
    const { text } = JSON.parse(body.data.body_source).t.data.props;
    return text === expected && body.data;
  }), { timeout: 10000 });
};
const sendTest = (id, subject) => {
  cy.visit(`/admin/campaigns/${id}`);
  cy.get('[data-cy=test-send] input[data-cy=test-emails]').type(`${subEmail}{enter}`);
  cy.intercept('POST', `/api/campaigns/${id}/test`).as('testSend');
  cy.get('[data-cy=test-send] [data-cy=btn-test-send]').click();
  cy.wait('@testSend').its('response.statusCode').should('eq', 200);

  const inbox = Cypress.env('mailpitUrl');
  expect(inbox, 'CYPRESS_mailpitUrl (the test inbox)').to.be.a('string').and.not.be.empty;
  return cy.waitUntil(() => cy.request(`${inbox}/api/v1/search?query=${encodeURIComponent(`subject:"${subject}"`)}`)
    .then(({ body }) => body.messages[0]?.ID), { timeout: 30000 })
    .then((messageId) => cy.request(`${inbox}/api/v1/message/${messageId}`)).its('body.HTML');
};

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
    sendTest(campaignId, `Format subject ${stamp}`).then((html) => {
      expect(html).to.contain(formatted);
      expect(html).to.contain('<span style="font-size:24px"><strong><u>안녕하세요, 김시험님.</u></strong></span><br/><strong>12월 3일까지</strong>');
      // LST-9: the button link is tracked and the view pixel is added.
      expect(html).to.not.contain(`href="https://example.test/promo-${stamp}"`);
      expect(html).to.match(/href="[^"]*\/link\/[^"]+"/);
      expect(html).to.match(/\/campaign\/[^"]+\/px\.png/);
    });
  });

  it('reopens and sends bold Markdown containing an HTML line break', () => {
    const source = '오늘 안내<br>내일 마감';
    const subject = `Markdown line break ${stamp}`;
    textCampaign(subject, source).then((id) => {
      openText(false, id, '오늘 안내');
      select(source);
      box().type('{ctrl}b');
      saveText(id, `<strong>${source}</strong>`);
      openText(true, id, '오늘 안내');
      box().find('strong').should('have.text', source);
      editorDoc().find('label:contains("Markdown") input').should('be.checked').and('be.disabled');

      sendTest(id, subject).then((html) => {
        expect(html, 'bold text and line break in received HTML')
          .to.match(/<strong>\s*오늘 안내\s*<br\s*\/?>\s*내일 마감\s*<\/strong>/);
      });
    });
  });

  it('groups typing at the same caret and undoes edits in different places separately', () => {
    const line = '신청 기간 12월 마감';
    textCampaign(`Undo navigation ${stamp}`, line, false).then((id) => {
      openText(false, id, line);
      box().type('{moveToEnd}!?{moveToStart}오타', { delay: 0 });
      box().should('have.text', `오타${line}!?`);
      box().type('{ctrl}z');
      box().should('have.text', `${line}!?`);
      saveText(id, `${line}!?`).its('body').should('contain', `${line}!?`);
      box().type('{ctrl}z');
      box().should('have.text', line);
    });
  });

  it('preserves the source and rendered code in lists and quotes when editing below it', () => {
    const example = '<span style="font-size:020px">예시</span>';
    const source = `안내\n\n-     ${example}\n\n> -     ${example}\n\n아래 문단`;
    textCampaign(`Markdown list code ${stamp}`, source).then((id) => {
      openText(false, id, '안내');
      box().type('{moveToEnd}!');
      saveText(id, `${source}!`).then(({ body }) => {
        const doc = new DOMParser().parseFromString(body, 'text/html');
        expect([...doc.querySelectorAll('li code')].map((el) => el.textContent.trim())).to.deep.equal([example, example]);
      });
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

  it('keeps a Setext heading and a thematic break when the whole block is made bold', () => {
    const head = '12월 회원 혜택';
    const source = `${head}\n==========\n\n첫 번째 안내입니다.\n\n---\n\n두 번째 안내입니다.`;
    textCampaign(`Format setext ${stamp}`, source).then((id) => {
      openText(false, id, head);
      editorDoc().find('#visual-editor-container h1').should('have.length', 1);
      editorDoc().find('#visual-editor-container hr').should('have.length', 1);
      box().then(($b) => {
        const el = $b[0];
        el.focus();
        const r = el.ownerDocument.createRange();
        r.selectNodeContents(el);
        el.ownerDocument.getSelection().removeAllRanges();
        el.ownerDocument.getSelection().addRange(r);
      });
      editorDoc().find('[data-format=bold]').click();
      saveText(id, `<strong>${head}</strong>\n==========\n\n<strong>첫 번째 안내입니다.</strong>\n\n---\n\n<strong>두 번째 안내입니다.</strong>`)
        .then(({ body }) => {
          const doc = new DOMParser().parseFromString(body, 'text/html');
          expect(doc.querySelectorAll('h1').length, 'heading').to.equal(1);
          expect(doc.querySelectorAll('hr').length, 'break').to.equal(1);
          expect(doc.body.textContent).to.not.match(/==========|---/);
        });
    });
  });

  it('shows a name inside code or a link address as plain text on the canvas', () => {
    const source = '쿠폰 `{{ .Subscriber.Name }}-2026`을 입력하세요.\n\n[내 페이지](https://example.test/{{.Subscriber.Name}}) '
      + '[혜택 보기](https://example.test/promo "환영 {{ .Subscriber.Name }}") {{ .Subscriber.Name }}님';
    textCampaign(`Format syntax ${stamp}`, source).then((id) => {
      cy.visit(`/admin/campaigns/${id}#content`);
      ready();
      cy.waitUntil(() => editorDoc().then((d) => Boolean(findBlock(d, '쿠폰'))), { timeout: 10000 });
      editorDoc().then((d) => {
        const block = findBlock(d, '쿠폰');
        expect(block.querySelector('code').innerHTML, 'code').to.not.include('<span');
        expect(block.querySelector('code').textContent).to.match(/^〔.+〕-2026$/);
        expect(block.querySelectorAll('a').length, 'links').to.equal(2);
        expect(block.textContent).to.not.include('](');
        expect(block.querySelectorAll('span[style*="background"]').length, 'chip outside syntax').to.equal(1);
      });
    });
  });

  it('inserts a name chip and keeps it when undoing and redoing formatting', () => {
    const greet = '안녕하세요 {{ .Subscriber.Name }}님';
    textCampaign(`Format undo ${stamp}`, '안녕하세요 님', false).then((id) => {
      openText(false, id, '안녕하세요');
      select('님');
      editorDoc().then((d) => d.getSelection().collapseToStart());
      box().parent().find('button[aria-haspopup=dialog]').click();
      editorDoc().find('[role=dialog]').contains(/^(Name|이름)$/).click();
      editorDoc().find('[role=dialog]').contains('button', /^(Insert|넣기)$/).click();
      box().find('[data-token="{{ .Subscriber.Name }}"]').should('exist');
      editorDoc().find('[role=dialog]').should('not.exist');

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
      saveText(id, greet).then(({ body_source: source }) => {
        expect(JSON.parse(source).t.data.props.markdown).to.equal(false);
      });
      box().type('{ctrl}y');
      box().find('strong [data-token]').should('exist');
      saveText(id, `<div class="axm-text"><strong>${greet}</strong></div>`);
    });
  });

  it('puts the caret back where an undone edit was and keeps the Markdown switch', () => {
    const md = { root: { type: 'EmailLayout', data: { childrenIds: ['t'] } }, t: { type: 'Text', data: { props: { text: '오늘 **마감**' }, style: { textAlign: 'center' } } } };
    cy.request('POST', '/api/campaigns', {
      name: `Format caret ${stamp}`, subject: 'caret', lists: [listId], type: 'regular', content_type: 'visual', messenger: 'email',
      body: '<p>x</p>', body_source: JSON.stringify(md),
    }).then(({ body: c }) => {
      openText(false, c.data.id, '오늘');
      editorDoc().find('label:contains("Markdown")').click();
      cy.get('button[data-cy=btn-save]').click();
      cy.wait(800);
      box().then(($b) => {
        const el = $b[0];
        el.focus();
        const r = el.ownerDocument.createRange();
        r.selectNodeContents(el);
        r.collapse(false);
        el.ownerDocument.getSelection().removeAllRanges();
        el.ownerDocument.getSelection().addRange(r);
        el.ownerDocument.execCommand('insertText', false, '!');
      });
      box().type('{ctrl}z');
      // The caret is back at the end: the next text goes there.
      editorDoc().then((d) => d.execCommand('insertText', false, '?'));
      cy.get('button[data-cy=btn-save]').click();
      cy.waitUntil(() => cy.request(`/api/campaigns/${c.data.id}`).then(({ body }) => {
        const t = JSON.parse(body.data.body_source).t.data;
        return t.props.text === '오늘 **마감**?' && t.props.markdown === true && t.style.textAlign === 'center';
      }), { timeout: 10000 });
    });
  });
});
