// Statistics dashboard (LST-64). The first part pins the screen with fixed API answers (the
// numbers are synthetic); the last test runs against the real server of the CI.
const NOW = new Date('2026-11-25T00:05:00Z').getTime(); // 11-25 09:05 KST (Wednesday)
const day = (n) => new Date(NOW - n * 86400000).toISOString();

const camp = (o = {}) => ({
  id: 1,
  name: 'Synthetic',
  subject: 'Synthetic subject',
  status: 'finished',
  started_at: day(10),
  sent: 1000,
  to_send: 1000,
  lists_n: 1,
  team_tags: ['팀:가'],
  viewers: 400,
  clickers: 80,
  hard: 10,
  soft: 10,
  complaint: 0,
  ledger_rows: 1000,
  overlap30: 20,
  tracking_off: false,
  followup_of: null,
  own: true,
  lists: [{ id: 1, name: '내 리스트' }],
  ...o,
});

const mine = [
  camp({ id: 10, name: 'Mine A', clickers: 80, started_at: day(30) }),
  camp({ id: 11, name: 'Mine B', clickers: 90, started_at: day(20) }),
  camp({ id: 12, name: 'Mine C', clickers: 100, started_at: day(12) }),
  camp({ id: 13, name: 'Mine fresh', clickers: 300, started_at: day(1) }),
  camp({ id: 14, name: 'Mine untracked', tracking_off: true, clickers: 0, viewers: 0, started_at: day(15) }),
];
const other = [
  camp({ id: 20, name: 'Other A', subject: 'Other team subject', team_tags: ['팀:나'], own: false, lists: undefined, hard: 40, soft: 10, clickers: 40, started_at: day(25) }),
  camp({ id: 21, name: 'Other B', team_tags: ['팀:나'], own: false, lists: undefined, hard: 40, soft: 10, clickers: 50, started_at: day(18) }),
  camp({ id: 22, name: 'Other C', team_tags: ['팀:나'], own: false, lists: undefined, hard: 40, soft: 10, clickers: 45, started_at: day(9) }),
  camp({ id: 23, name: 'Small team', team_tags: ['팀:다'], own: false, lists: undefined, sent: 71, clickers: 8, hard: 3, soft: 0, complaint: 1, started_at: day(5) }),
  camp({ id: 24, name: 'Running now', status: 'running', team_tags: ['팀:가'], sent: 300, to_send: 900, started_at: day(0) }),
];

const answer = (campaigns) => ({
  data: {
    generated_at: new Date(NOW).toISOString(),
    my_team: '팀:가',
    teams: [{ name: '가', tag: '팀:가', lists: 2 }, { name: '나', tag: '팀:나', lists: 1 }, { name: '다', tag: '팀:다', lists: 1 }, { name: '라', tag: '팀:라', lists: 3 }],
    campaigns,
  },
});

const stub = ({ cur = [...mine, ...other], prev = [], recipientTracked = true, overlapTotals = [{ id: 0, team: '', recipients: 200, overlap30: 200, overlap7: 200 }] } = {}) => {
  cy.intercept('GET', '/api/stats/campaigns*', (req) => {
    const from = new Date(req.query.from).getTime();
    // The first call is the period, the second the period before it.
    if (req.query.overlap === 'true') {
      const perCampaign = cur.filter((c) => c.sent > 0 && c.status === 'finished').map((c) => ({
        id: c.id, ledger_rows: c.ledger_rows, overlap30: c.overlap30, overlap7: 10,
      }));
      req.reply({ data: [...perCampaign, ...overlapTotals] });
      return;
    }
    const previous = from < NOW - 100 * 86400000;
    req.reply(previous && prev === null ? { statusCode: 503 } : answer(previous ? prev : cur));
  }).as('stats');
  cy.intercept('GET', '/api/stats/recipients*', (req) => {
    const by = req.query.by;
    const groups = by === 'age'
      ? [{ key: 'new7', sent: 1500, recipients: 1500, opened: 300, clicked: 60, bounced: 60 }, { key: 'older', sent: 4000, recipients: 4000, opened: 1800, clicked: 400, bounced: 40 }]
      : [{ key: 'KR', sent: 4000, recipients: 4000, opened: 1800, clicked: 380, bounced: 50 }, { key: '__other__', sent: 20, recipients: 20, opened: 5, clicked: 1, bounced: 0 }];
    req.reply({ data: ['', '__none__'].flatMap((team) => groups.map((group) => ({
      team, ...group,
      tracked_sent: recipientTracked ? group.sent : 0,
      tracked_recipients: recipientTracked ? group.recipients : 0,
      opened: recipientTracked ? group.opened : 0,
      clicked: recipientTracked ? group.clicked : 0,
    }))) });
  });
};

// The login form redirects through a new document. Install the clock on the
// authenticated visit so the page creates its first requests with the fixed date.
const visitWithFixedClock = (url = '/admin') => {
  cy.loginAndVisit('/admin/lists');
  cy.clock(NOW, ['Date']);
  cy.visit(url);
  cy.window().then((win) => expect(win.Date.now()).to.equal(NOW));
};

describe('Statistics dashboard (fixed answers)', () => {
  it('shows the team table first, with the baselines, small samples and teams that sent nothing', () => {
    stub();
    visitWithFixedClock();
    cy.wait('@stats');

    cy.get('[data-cy=stats-teams]').should('contain', 'Team comparison');
    // The first block on the page is the team table.
    cy.get('[data-cy=stats-summary]').should('contain', '가 click rate');
    // Team 가: 3 campaigns count (fresh, untracked and running are left out of the rates).
    cy.get('[data-cy="team-row-팀:가"]').should('contain', 'My team').and('contain', '3 counted in rates')
      .and('contain', '1 still collecting').and('contain', '1 untracked');
    cy.get('[data-cy="team-row-팀:가"]').should('contain', '9.0%'); // 270 / 3000
    // Team 나: bounce 5.0% is over the 2% line, shown with the warning mark.
    cy.get('[data-cy="team-row-팀:나"]').find('.st-over').should('contain', '5.0%').and('contain', '⚠');
    // A team with too few messages is grey and says so; a team without sends is a "no sends" row.
    cy.get('[data-cy="team-row-팀:다"]').should('contain', 'Small sample');
    cy.get('[data-cy="team-row-팀:라"]').should('contain', 'No sends').and('contain', '3 lists');
    cy.get('[data-cy=team-row-all]').should('contain', 'All teams');
    // The old 30-day charts are gone.
    cy.contains('Campaign views').should('not.exist');
  });

  it('explains the figures with a "?" and folds the definitions', () => {
    stub();
    visitWithFixedClock();
    cy.wait('@stats');
    cy.get('[data-cy=help-click]').first().trigger('mouseenter', { force: true });
    cy.contains('Share of recipients who clicked a link at least once').should('exist');
    cy.get('[data-cy=defs-table]').should('not.exist');
    cy.get('[data-cy=defs-link]').click({ force: true });
    cy.get('[data-cy=defs-table]').should('contain', 'In one line').and('contain', 'Small sample');
    // Formula and minimum sample are in the opened row only.
    cy.get('[data-cy=defs-table]').should('not.contain', 'Minimum sample · 200 sends per campaign');
    cy.get('[data-cy=def-click]').click();
    cy.get('[data-cy=defs-table]').should('contain', 'Minimum sample · 200 sends per campaign');
  });

  it('filters the campaigns by the team row and keeps other teams closed', () => {
    stub();
    visitWithFixedClock();
    cy.wait('@stats');
    cy.get('[data-cy=camp-more]').click(); // six rows are shown at first
    // Other teams: name and numbers only, no button to open; own campaigns have the buttons.
    cy.get('[data-cy=camp-row-20]').should('contain', 'Other A').and('contain', 'Other team subject').and('contain', 'Cannot open or clone').and('have.attr', 'data-own', 'false');
    cy.get('[data-cy=camp-row-20] a').should('not.exist');
    cy.get('[data-cy=camp-row-12] a').should('have.length', 3).then(($links) => {
      expect([...$links].map((link) => link.dataset.cy)).to.deep.eq([
        'result-analytics', 'result-clone', 'result-followup',
      ]);
    });
    // The fresh one says it is collecting and is not compared; the running one is left out.
    cy.get('[data-cy=camp-row-13]').should('contain', 'Collecting').and('contain', 'Not compared');
    cy.get('[data-cy=camp-row-24]').should('contain', 'Running');
    cy.get('[data-cy=camp-row-14]').should('contain', 'Not tracked').and('contain', 'Not 0%');
    // Own campaign compared with the median of its team (3 campaigns).
    cy.get('[data-cy=camp-row-12]').should('contain', '가 median 9.0%').and('not.contain', 'All teams median');
    cy.get('[data-cy=camp-row-23] .st-over').should('not.exist');

    cy.get('[data-cy="team-row-팀:나"]').find('button[aria-pressed=false]').focus().should('be.focused').click();
    cy.get('[data-cy=team-filter]').should('contain', '나');
    cy.get('[data-cy=camp-row-20]').should('exist');
    cy.get('[data-cy=camp-row-12]').should('not.exist');
    cy.location('search').should('include', 'team=');
    cy.get('[data-cy=stats-insights]').should('contain', 'recalculated from 나 only');
    cy.get('[data-cy=team-filter] a').click();
    cy.get('[data-cy=camp-row-12]').should('exist');
  });

  it('shows disabled periods and one cumulative card for a user without analytics access', () => {
    cy.intercept('GET', '/api/profile', (request) => {
      request.continue(({ body }) => {
        body.data.user_role = { id: 9, permissions: ['lists:get_all'] };
      });
    });
    cy.intercept('GET', '/api/dashboard/counts', {
      data: {
        updated_at: day(0), lists: { total: 14 }, subscribers: { total: 3437 }, campaigns: { total: 13 }, messages: 48320,
      },
    });
    cy.loginAndVisit('/');
    cy.get('[data-cy=stats-noperm]').should('be.visible');
    cy.get('[data-cy=period] button').should('have.length', 4).and('be.disabled');
    cy.get('.dashboard header').should('contain', 'From listmonk records').and('contain', '09:05 KST')
      .and('contain', 'may differ from the Slack alerts');
    cy.get('[data-cy=defs-link]').should('contain', 'Metric definitions').and('have.attr', 'aria-disabled', 'true');
    cy.get('[data-cy=counts-summary]').should('have.length', 1).and('contain', 'All-time totals')
      .and('contain', '14').and('contain', '3,437').and('contain', '13').and('contain', '48,320');
    cy.get('.dashboard').find('[data-cy=counts-toggle], [data-cy=lists], [data-cy=stats-teams]').should('not.exist');
  });

  // A failed counts request is not "0 lists, 0 subscribers": the card says it failed.
  it('does not show zeros when the counts cannot be loaded', () => {
    cy.intercept('GET', '/api/profile', (request) => {
      request.continue(({ body }) => {
        body.data.user_role = { id: 9, permissions: ['lists:get_all'] };
      });
    });
    cy.intercept('GET', '/api/dashboard/counts', { statusCode: 503, body: { message: 'down' } });
    cy.loginAndVisit('/');
    cy.get('[data-cy=counts-failed]').should('be.visible').and('contain', 'could not be loaded');
    cy.get('[data-cy=counts-summary]').should('not.contain', 'Lists').and('not.contain', '0');
  });

  // LST-73: the counts are company-wide, so a role that reads nothing across lists, subscribers
  // or campaigns does not get them (the server answers 403 as well, see the API spec below).
  it('shows no counts and asks for none for a user who reads nothing company-wide', () => {
    cy.intercept('GET', '/api/profile', (request) => {
      request.continue(({ body }) => {
        body.data.user_role = { id: 9, permissions: ['campaigns:get', 'subscribers:get'] };
      });
    });
    cy.intercept('GET', '/api/dashboard/counts').as('counts');
    cy.loginAndVisit('/');
    cy.get('[data-cy=stats-noperm]').should('be.visible');
    cy.get('[data-cy=counts-summary]').should('not.exist');
    cy.get('@counts.all').should('have.length', 0);
  });

  it('next-send cards say why a group is too small and hide other teams\' lists', () => {
    stub();
    visitWithFixedClock();
    cy.wait('@stats');
    cy.get('[data-cy=card-list]').should('contain', '내 리스트').and('contain', 'Lists outside your access').and('contain', 'Bounce 2.0%');
    cy.get('[data-cy=card-weekday] [data-cy=concl-weekday]').should('contain', '3 campaigns');
    cy.get('[data-cy=card-age]').should('contain', 'New (within 7 days of sending)');
    // A country group under 30 recipients is merged into "other".
    cy.get('[data-cy=card-country]').should('contain', 'Other (groups below 30 people)');
    cy.get('[data-cy=card-followup]').should('contain', 'No campaigns have a completed follow-up yet');
    cy.get('[data-cy=insight-followup-start]').should('have.attr', 'href', '/admin/campaigns/10');
    cy.get('[data-cy=concl-age]').should('contain', '30 days or older')
      .and('contain', 'click rate -6.0%p').and('contain', 'bounce rate +3.0%p');
    cy.get('[data-cy=card-overlap]').should('contain', 'previous 30 days').and('contain', 'Previous 7 days');
    cy.get('[data-cy=card-overlap]').should('contain', 'campaigns, combined').and('contain', 'send records');
    cy.get('[data-cy=card-country]').should('contain', 'Bounce 1.3%');
    cy.get('[data-cy=card-weekday]').should('contain', 'Bounce');
    cy.get('[data-cy=card-hour]').should('contain', 'Bounce');
    cy.get('[data-cy=card-length]').should('contain', 'Bounce');
    cy.get('[data-cy=stats-insights]').should('not.contain', '@');
    cy.contains('[data-cy=stats-insights] button', 'Country').click();
    cy.get('[data-cy=card-list]').should('not.exist');
    cy.get('[data-cy=insight-all]').click();
    cy.get('[data-cy=card-list]').should('exist');
  });

  it('compares eligible weekday, hour and subject-length groups by click-rate difference', () => {
    const camps = [
      ...[7, 14, 21].map((d) => camp({
        id: d, started_at: day(d), clickers: 100, subject: 'Short',
      })),
      ...[6, 13, 20].map((d) => camp({
        id: d, started_at: day(d).replace('T00:', 'T11:'), clickers: 50, subject: 'A medium length subject line',
      })),
      camp({
        id: 10, started_at: day(10).replace('T00:', 'T05:'), clickers: 900,
        subject: 'An exceptionally long subject to exercise the small-sample state',
      }),
    ];
    stub({ cur: camps });
    visitWithFixedClock();
    [
      ['weekday', ['Wed', 'Thu']],
      ['hour', ['06', '18']],
      ['length', ['20', '21']],
    ].forEach(([key, labels]) => {
      cy.get(`[data-cy=concl-${key}]`).should('contain', labels[0]).and('contain', labels[1])
        .and('contain', '+5.0%p');
    });
  });

  it('uses no-team groups and the actual follow-up audience and combined denominator', () => {
    const followup = camp({
      id: 80, team_tags: [], followup_of: 70, followup_parent_sent: 2000,
      followup_condition: { open: 'any', click: 'any' }, combined_clickers: 120, combined_viewers: 550,
    });
    stub({ cur: [...mine, followup] });
    visitWithFixedClock();
    cy.get('[data-cy=team-row-__none__] button[aria-pressed=false]').click();
    cy.get('[data-cy=team-filter]').should('contain', 'No team');
    cy.get('[data-cy=camp-row-80]').within(() => {
      cy.get('[data-cy=result-clone]').should('exist');
      cy.get('[data-cy=result-followup]').should('not.exist');
    });
    cy.get('[data-cy=card-age]').should('contain', 'Unique recipients 1,500');
    cy.get('[data-cy=card-country]').should('contain', 'Unique recipients 4,000');
    cy.get('[data-cy=card-followup]')
      .should('contain', 'Combined · click 6.0% · open 27.5%')
      .and('contain', '120 unique clickers ÷ 2,000 original sends')
      .and('contain', 'Audience');
    cy.get('[data-cy=card-followup]').should('not.contain', 'not respond');
  });

  it('compares the same team across adjacent periods and explains the active numbers', () => {
    const prev = [
      ...mine.slice(0, 3).map((c) => ({ ...c, started_at: day(105), clickers: 10 })),
      ...other.slice(0, 3).map((c) => ({ ...c, started_at: day(105), clickers: 170 })),
    ];
    stub({ prev });
    visitWithFixedClock();
    cy.get('[data-cy=stats-summary]').should('contain', 'Previous period for the same team 1.0% (+8.0%p)');
    cy.get('[data-cy="team-row-팀:가"]').should('contain', '+8.0%p')
      .find('[data-cy=help-click]').trigger('mouseenter', { force: true });
    cy.get('[data-cy="team-row-팀:가"] [data-cy=help-click]')
      .should('contain', '270 ÷ 3,000 sent (3 campaigns)').and('contain', 'Vertical line').and('contain', 'Previous period · 1.0% → ▲ +8.0%p').and('contain', '±0.5%p');
  });

  it('shows an increase at an exact 0.5 percentage point change in the row and help', () => {
    const cur = mine.slice(0, 3).map((c) => ({ ...c, clickers: 9 }));
    const prev = cur.map((c) => ({ ...c, started_at: day(105), clickers: 4 }));
    stub({ cur, prev });
    visitWithFixedClock();
    const row = '[data-cy="team-row-팀:가"]';
    cy.get(row).should('contain', 'vs prev. ▲ +0.5%p');
    cy.get(`${row} [data-cy=help-click] button`).focus();
    cy.get(`${row} [data-cy=help-click] .tooltip-content`)
      .should('be.visible').and('contain', 'Previous period · 0.4% → ▲ +0.5%p');
  });

  it('distinguishes an unreadable previous period from absent sends and requests all time', () => {
    stub({ prev: null });
    visitWithFixedClock();
    cy.get('[data-cy=stats-summary]').should('contain', 'Previous period results could not be loaded').and('not.contain', 'No sends');
    cy.intercept('GET', '/api/stats/campaigns*', (req) => {
      if (req.query.from === 'all' && req.query.overlap !== 'true') {
        req.reply(answer([...mine, camp({ id: 900, name: 'Historic campaign', started_at: day(900) })]));
      }
    }).as('allStats');
    cy.get('[data-cy=period-all]').click();
    cy.wait('@allStats').its('request.query.from').should('eq', 'all');
    cy.get('[data-cy=camp-row-900]').should('contain', 'Historic campaign');
    cy.get('[data-cy="team-row-팀:가"]').click();
    cy.wait('@allStats').its('request.query.from').should('eq', 'all');
    cy.get('[data-cy=team-filter]').should('contain', '가');
    cy.get('[data-cy=camp-row-900]').should('exist');
  });

  it('shows the comparison requirement and distinguishes not tracked from zero percent', () => {
    const untracked = camp({ id: 30, team_tags: ['팀:라'], viewers: 0, clickers: 0 });
    stub({ cur: [...mine, untracked] });
    visitWithFixedClock();
    cy.get('[data-cy=team-comparison-start]').should('contain', '3 eligible campaigns and 1,000 sends');
    cy.get('[data-cy="team-row-팀:라"]').should('contain', 'Not tracked').and('not.contain', '0.0%');
    cy.get('[data-cy=camp-row-30]').should('contain', 'Not tracked');
  });

  it('shows tracking gaps in recipient cards while retaining bounce rates', () => {
    stub({ recipientTracked: false });
    visitWithFixedClock();
    cy.get('[data-cy=card-country]').should('contain', 'Not tracked').and('contain', 'Bounce 1.3%').and('not.contain', 'Click 0.0%');
    cy.get('[data-cy=card-age]').should('contain', 'Not tracked').and('contain', 'Bounce 4.0%').and('not.contain', 'Open 0.0%');
  });

  it('separates the tracked sample from the full audience in both recipient cards', () => {
    const sparse = { team: '', sent: 601, recipients: 200, tracked_sent: 1, tracked_recipients: 1, opened: 1, clicked: 1, bounced: 3 };
    const enough = { team: '', sent: 200, recipients: 200, tracked_sent: 200, tracked_recipients: 200, opened: 30, clicked: 10, bounced: 0 };
    stub();
    cy.intercept('GET', '/api/stats/recipients*', (req) => req.reply({
      data: req.query.by === 'country'
        ? [{ ...sparse, key: 'KR' }, { ...enough, key: 'JP' }, { ...enough, key: 'US', clicked: 8 },
          { ...enough, key: '(none)', clicked: 30 }]
        : [{ ...sparse, key: 'older' }],
    }));
    visitWithFixedClock();
    const smallRows = [
      ['[data-cy=card-country] .st-br', 'KR'],
      ['[data-cy=card-age] .st-br', '30 days or older'],
    ];
    smallRows.forEach(([selector, label]) => {
      cy.contains(selector, label).should('contain', 'Small tracking sample')
        .and('contain', 'Tracked sample · Unique recipients 1 · Sends 1')
        .and('contain', '200 unique recipients overall')
        .and('contain', 'Bounce 0.5%')
        .and('not.contain', '100.0%');
    });
    cy.get('[data-cy=concl-country]').should('contain', 'JP has the highest click rate at 5.0%');
    cy.contains('[data-cy=card-country] .st-br', 'Unknown').should('contain', 'Click 15.0%').and('not.contain', 'Small sample');
    cy.contains('[data-cy=card-country] .st-br', 'JP').should('contain', 'Tracked sample · Unique recipients 200 · Sends 200');
    cy.get('[data-cy=concl-age]').should('contain', '200 tracked recipients');
  });

  it('uses the scoped unique recipient count for period overlap', () => {
    const teams = [
      { id: 0, team: '', recipients: 300, overlap30: 200, overlap7: 200 },
      { id: 0, team: '팀:가', recipients: 200, overlap30: 200, overlap7: 200 },
    ];
    const repeated = mine.slice(0, 3).map((c, index) => ({
      ...c, sent: 200, ledger_rows: 200, overlap30: index ? 200 : 0,
    }));
    stub({ cur: [...repeated, camp({ id: 200, team_tags: ['팀:나'], sent: 100, ledger_rows: 100, overlap30: 0 })], overlapTotals: teams });
    visitWithFixedClock();
    cy.get('[data-cy=concl-overlap]').should('contain', '66.7% (200 of 300 unique recipients)');
    cy.get('[data-cy="team-row-팀:가"]').click();
    cy.get('[data-cy=concl-overlap]').should('contain', '100.0% (200 of 200 unique recipients)').and('not.contain', '400');
  });

  it('keeps company warnings muted below three campaigns', () => {
    stub({ cur: [camp({ hard: 30, soft: 0, complaint: 2 })] });
    visitWithFixedClock();
    cy.get('[data-cy=team-row-all]').should('contain', '3.0%').and('contain', '0.20%').find('.st-over').should('not.exist');
    cy.get('[data-cy="team-row-팀:가"] .st-over').should('not.exist');
  });

  it('shows campaign results while the overlap calculation is pending', () => {
    stub();
    cy.intercept({ method: 'GET', pathname: '/api/stats/campaigns', query: { overlap: 'true' } }, {
      delay: 1500,
      body: { data: [] },
    }).as('overlap');
    visitWithFixedClock();
    cy.get('[data-cy=stats-teams]').should('be.visible');
    cy.get('[data-cy=card-overlap]').should('contain', 'Calculating send overlap');
    cy.wait('@overlap');
    cy.get('[data-cy=card-overlap]').should('contain', 'at least 95%');
  });

  it('shows the empty "no sends" rows when no campaign was sent', () => {
    stub({ cur: [] });
    visitWithFixedClock();
    cy.wait('@stats');
    cy.get('[data-cy="team-row-팀:가"]').should('contain', 'No sends');
    cy.get('[data-cy=stats-campaigns]').should('exist');
  });
});

describe('Result summary on the campaign and analytics screens (fixed answers)', () => {
  const finish = (res) => {
    res.body.data.status = 'finished';
    res.body.data.started_at = day(900);
    res.body.data.sent = 1000;
    res.body.data.to_send = 1000;
  };

  it('shows the result of a finished campaign on top of its screen', () => {
    stub();
    visitWithFixedClock('/admin/campaigns');
    cy.request('/api/campaigns?per_page=1').then((r) => {
      const real = r.body.data.results[0].id;
      // The campaign of the server stands in for #12 of the answer: finished 900 days ago.
      cy.request(`/api/campaigns/${real}`).then(({ body }) => {
        const data = { ...body.data, id: 12 };
        finish({ body: { data } });
        cy.intercept('GET', `/api/campaigns/${real}`, { data });
        cy.intercept('GET', '/api/campaigns/12', { data });
      });
      cy.intercept('GET', '/api/stats/campaigns*', (req) => req.reply(answer(
        mine.map((c) => ({ ...c, started_at: day(c.id === 12 ? 900 : 910) })),
      ))).as('stats2');
      cy.visit(`/admin/campaigns/${real}`);
      cy.get('[data-cy=result-summary]').should('contain', 'Result summary').and('contain', '10.0%');
      cy.get('[data-cy=result-compare]').should('contain', '가 median');
      cy.get('[data-cy=result-analytics]').should('exist');
      cy.get('[data-cy=result-clone]').click();
      cy.get('[data-cy=clone-form]').should('be.visible');
      cy.get('[data-cy=clone-close]').click();
      cy.get('[data-cy=result-followup]').click();
      cy.get('[data-cy=reaction-list]').should('be.visible');
    });
  });
  it('agrees with proof v1 when a sent campaign has no open or click records', () => {
    const zero = camp({ id: 52, viewers: 0, clickers: 0, hard: 2, soft: 1, complaint: 1 });
    stub({ cur: [zero] });
    cy.intercept('GET', '/api/campaigns/52', {
      data: {
        ...zero, created_at: day(11), uuid: '4d9b346a-2085-4d49-9257-fca74821f256', lists: [],
      },
    });
    cy.intercept('GET', '/api/campaigns/analytics/views*', { data: [] });
    cy.intercept('GET', '/api/campaigns/analytics/clicks*', { data: [] });
    cy.intercept('GET', '/api/campaigns/analytics/bounces*', { data: [{ campaign_id: 52, count: 4, timestamp: day(9) }] });
    cy.intercept('GET', '/api/campaigns/analytics/links*', { data: [] });
    visitWithFixedClock('/admin/campaigns/analytics?id=52');
    cy.get('[data-cy=result-click]').should('contain', 'Not tracked');
    cy.get('[data-cy=btn-proof-summary]').click();
    cy.get('[data-cy=proof-summary-text]').invoke('val')
      .should('contain', '발송 1,000 · 열람 추적 없음 · 클릭 추적 없음 · 반송 4');
  });

  it('hides empty charts and queries from the actual start for a recent campaign', () => {
    const tracked = camp({ id: 91, name: 'Tracked', viewers: 431, clickers: 89, hard: 10, soft: 5 });
    const untracked = camp({ id: 92, name: 'Untracked', viewers: 0, clickers: 0 });
    stub({ cur: [tracked, untracked] });
    visitWithFixedClock('/admin/campaigns/analytics');
    cy.get('[data-cy=analytics-empty]').should('be.visible');
    cy.get('.charts').should('not.exist');
    cy.contains('[data-cy=analytics-empty] .st-card > div', 'Tracked')
      .should('contain', 'Click 8.9%').and('contain', 'Open 43.1%').and('contain', 'Bounce 1.5%');
    cy.contains('[data-cy=analytics-empty] .st-card > div', 'Untracked')
      .should('contain', 'Not tracked').and('contain', 'Bounce 2.0%').and('not.contain', 'Open 0.0%');
    cy.request('/api/campaigns?per_page=1').then(({ body }) => {
      const { id } = body.data.results[0];
      const started = new Date(NOW - 5 * 86400000 + 61000).toISOString();
      cy.intercept('GET', `/api/campaigns/${id}`, (req) => req.continue((res) => {
        finish(res);
        res.body.data.started_at = started;
      }));
      cy.intercept('GET', '/api/campaigns/analytics/views?*', { data: [] }).as('views');
      cy.visit(`/admin/campaigns/analytics?id=${id}`);
      cy.wait('@views').then(({ request }) => {
        expect(new Date(request.query.from).getTime()).to.eq(new Date(started).getTime());
      });
      cy.get('.charts').should('be.visible');
    });
  });

  it('uses the selected start date for recent and older campaigns from autocomplete', () => {
    const choices = [
      camp({ id: 91, name: 'Recent synthetic', started_at: '2026-11-20T05:23:00.000Z' }),
      camp({ id: 92, name: 'Older synthetic', started_at: '2026-08-27T01:00:00.000Z' }),
    ];
    stub({ cur: choices });
    cy.intercept('GET', '/api/stats/campaigns*', answer(choices));
    cy.intercept({ method: 'GET', pathname: '/api/campaigns' }, { data: { results: choices } });
    cy.intercept('GET', '/api/campaigns/analytics/views?*', { data: [] }).as('autocompleteViews');
    visitWithFixedClock('/admin/campaigns/analytics');
    choices.forEach((choice, index) => {
      cy.intercept('GET', `/api/campaigns/${choice.id}`, { data: choice });
      if (index) cy.visit('/admin/campaigns/analytics');
      cy.get('[data-cy=from] input').should('have.value', '');
      cy.get('[data-cy=to] input').should('have.value', '');
      cy.get('.taginput input').type(choice.name);
      cy.contains('.dropdown-item', choice.name).click();
      cy.get('[data-cy=btn-search]').click();
      cy.wait('@autocompleteViews').then(({ request }) => {
        expect(request.query.from).to.eq(choice.started_at);
        expect(request.query.to).to.eq(new Date(NOW).toISOString());
      });
      cy.location('search').should('not.include', 'from=');
      cy.get('[data-cy=result-summary]').find('[data-cy=result-analytics], [data-cy=result-clone], [data-cy=result-followup]').should('have.length', 3);
    });
  });

  it('preserves explicit dates when picking a recent campaign and restores the picker when cleared', () => {
    const recent = camp({ id: 91, started_at: day(2) });
    const from = day(5);
    const to = day(4);
    const query = `from=${Date.parse(from) / 1000}&to=${Date.parse(to) / 1000}`;
    stub({ cur: [recent] });
    cy.intercept('GET', '/api/campaigns/91', { data: { ...recent, created_at: day(3) } });
    cy.intercept('GET', '/api/campaigns/analytics/views?*', { data: [] }).as('explicitViews');
    visitWithFixedClock('/admin/campaigns/analytics');
    cy.get('[data-cy=from] input').should('have.attr', 'placeholder', 'Blank = campaign start');
    cy.get('[data-cy=to] input').should('have.attr', 'placeholder', 'Blank = now');
    cy.get('[data-cy=proof-summary]').should('not.exist');
    cy.visit(`/admin/campaigns/analytics?${query}`);
    cy.get('[data-cy=pick-91]').click();
    cy.wait('@explicitViews').then(({ request }) => {
      expect(request.query.from).to.eq(from);
      expect(request.query.to).to.eq(to);
    });
    cy.location('search').should('include', query);
    cy.get('[data-cy=proof-summary]').should('be.visible');
    cy.get('.taginput .tag .delete').click();
    cy.get('[data-cy=pick-91]').should('be.visible');
    cy.get('[data-cy=proof-summary]').should('not.exist');
    cy.get('[data-cy=from] input').invoke('val').should('not.eq', '');
    cy.location('search').should('eq', `?${query}`);
    cy.get('[data-cy=pick-91]').click();
    cy.wait('@explicitViews').its('request.query.from').should('eq', from);
  });


});

describe('Statistics against the server', () => {
  it('answers the statistics API for an administrator and keeps the dashboard counts', () => {
    cy.loginAndVisit('/');
    // The real queries read every campaign of the database the other specs filled: wait longer.
    cy.get('[data-cy=stats-teams]', { timeout: 30000 }).should('exist');
    cy.request('/api/stats/campaigns').its('body.data.campaigns').should('be.an', 'array');
    cy.request('/api/stats/recipients?by=country').its('body.data').should('be.an', 'array');
    cy.request('/api/dashboard/counts').its('body.data.updated_at').should('match', /^\d{4}-\d{2}-\d{2}T/);
    cy.request({ url: '/api/stats/recipients?by=x', failOnStatusCode: false }).its('status').should('eq', 400);
    // The folded counts stay in the page.
    cy.get('[data-cy=lists] .title').should('exist');
    cy.get('[data-cy=counts-toggle]').click();
    cy.get('[data-cy=lists]').should('be.visible');
  });
});
