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
const stored = '<div class="axm-text">안녕하세요, {{ .Subscriber.Name }}님.<br><strong>12월 3일까지</strong> 신청하면 '
  + '<span style="font-size:20px"><strong><u>수수료가 0원입니다</u></strong></span>. 놓치지 마세요.</div>';
const formatted = '<span style="font-size:20px"><strong><u>수수료가 0원입니다</u></strong></span>';

let campaignId;

const editorDoc = () => cy.get('#visual-editor').its('0.contentDocument');
const ready = () => cy.waitUntil(() => cy.get('#visual-editor').then(($f) => Boolean($f[0].contentWindow.EmailBuilder
  && $f[0].contentWindow.EmailBuilder.isRendered('visual-editor-container')
  && $f[0].contentDocument.querySelector('#visual-editor-container h2'))), { timeout: 20000 });
const openText = (reload = false) => {
  // A visit that only changes the hash does not load the page again.
  if (reload) cy.reload();
  else cy.visit(`/admin/campaigns/${campaignId}#content`);
  ready();
  editorDoc().then((d) => {
    [...d.querySelectorAll('#visual-editor-container div')].reverse()
      .find((el) => el.textContent.startsWith('안녕하세요')).click();
  });
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
const canvasText = () => editorDoc().then((d) => [...d.querySelectorAll('#visual-editor-container div')].reverse()
  .find((el) => el.textContent.startsWith('안녕하세요')));

describe('Visual editor inline formatting', () => {
  before(() => {
    cy.loginAndVisit('/admin/campaigns');
    cy.request('POST', '/api/lists', {
      name: `Format ${stamp}`, type: 'private', optin: 'single', no_team: true,
    }).then(({ body: list }) => {
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

    select('수수료가 0원입니다');
    editorDoc().find('[data-format=bold]').click();
    editorDoc().find('[data-format=underline]').click();
    editorDoc().find('[data-format=size]').parent().click();
    editorDoc().find('li[role=option]').contains('20px').click();
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
      expect([...el.querySelectorAll('span')].some((s) => s.style.backgroundColor === 'rgb(232, 242, 255)'), 'chip').to.equal(true);
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
        .and.contain('{{ .Subscriber.Name }}님.<br/>');
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
      expect([...el.querySelectorAll('u')]).to.have.length(0);
      expect([...el.querySelectorAll('span')].filter((s) => s.style.fontSize === '20px')).to.have.length(0);
      expect([...el.querySelectorAll('strong')].map((s) => s.textContent)).to.deep.equal(['12월 3일까지']);
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
      expect(html).to.contain('안녕하세요, 김시험님.<br/><strong>12월 3일까지</strong>');
      // LST-9: the button link is tracked and the view pixel is added.
      expect(html).to.not.contain(`href="https://example.test/promo-${stamp}"`);
      expect(html).to.match(/href="[^"]*\/link\/[^"]+"/);
      expect(html).to.match(/\/campaign\/[^"]+\/px\.png/);
    });
  });
});
