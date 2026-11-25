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

const stub = ({ cur = [...mine, ...other], prev = [] } = {}) => {
  cy.clock(NOW, ['Date']);
  cy.intercept('GET', '/api/stats/campaigns*', (req) => {
    const from = new Date(req.query.from).getTime();
    // The first call is the period, the second the period before it.
    const previous = from < NOW - 100 * 86400000;
    req.reply(previous && prev === null ? { statusCode: 503 } : answer(previous ? prev : cur));
  }).as('stats');
  cy.intercept('GET', '/api/stats/recipients*', (req) => {
    const by = req.query.by;
    const groups = by === 'age'
      ? [{ key: 'new7', sent: 1500, recipients: 1500, opened: 300, clicked: 60, bounced: 60 }, { key: 'older', sent: 4000, recipients: 4000, opened: 1800, clicked: 400, bounced: 40 }]
      : [{ key: 'KR', sent: 4000, recipients: 4000, opened: 1800, clicked: 380, bounced: 50 }, { key: '__other__', sent: 20, recipients: 20, opened: 5, clicked: 1, bounced: 0 }];
    req.reply({ data: ['', '__none__'].flatMap((team) => groups.map((group) => ({ team, ...group }))) });
  });
};

describe('Statistics dashboard (fixed answers)', () => {
  it('shows the team table first, with the baselines, small samples and teams that sent nothing', () => {
    stub();
    cy.loginAndVisit('/');
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
    cy.loginAndVisit('/');
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
    cy.loginAndVisit('/');
    cy.wait('@stats');
    cy.get('[data-cy=camp-more]').click(); // six rows are shown at first
    // Other teams: name and numbers only, no button to open; own campaigns have the buttons.
    cy.get('[data-cy=camp-row-20]').should('contain', 'Other A').and('contain', 'Other team subject').and('contain', 'Cannot open or clone').and('have.attr', 'data-own', 'false');
    cy.get('[data-cy=camp-row-20] a').should('not.exist');
    cy.get('[data-cy=camp-row-12] a').should('exist');
    // The fresh one says it is collecting and is not compared; the running one is left out.
    cy.get('[data-cy=camp-row-13]').should('contain', 'Collecting').and('contain', 'Not compared');
    cy.get('[data-cy=camp-row-24]').should('contain', 'Running');
    cy.get('[data-cy=camp-row-14]').should('contain', 'Not tracked').and('contain', 'Not 0%');
    // Own campaign compared with the median of its team (3 campaigns).
    cy.get('[data-cy=camp-row-12]').should('contain', 'All teams median');
    cy.get('[data-cy=camp-row-23] .st-over').should('not.exist');

    cy.get('[data-cy="team-row-팀:나"]').click();
    cy.get('[data-cy=team-filter]').should('contain', '나');
    cy.get('[data-cy=camp-row-20]').should('exist');
    cy.get('[data-cy=camp-row-12]').should('not.exist');
    cy.location('search').should('include', 'team=');
    cy.get('[data-cy=stats-insights]').should('contain', 'recalculated from 나 only');
    cy.get('[data-cy=team-filter] a').click();
    cy.get('[data-cy=camp-row-12]').should('exist');
  });

  it('next-send cards say why a group is too small and hide other teams\' lists', () => {
    stub();
    cy.loginAndVisit('/');
    cy.wait('@stats');
    cy.get('[data-cy=card-list]').should('contain', '내 리스트').and('contain', 'Lists outside your access').and('contain', 'Bounce 2.0%');
    cy.get('[data-cy=card-weekday] [data-cy=concl-weekday]').should('contain', '3 campaigns');
    cy.get('[data-cy=card-age]').should('contain', 'New (within 7 days of sending)');
    // A country group under 30 recipients is merged into "other".
    cy.get('[data-cy=card-country]').should('contain', 'Other (groups below 30 people)');
    cy.get('[data-cy=card-followup]').should('contain', 'No campaigns have a completed follow-up yet');
    cy.get('[data-cy=card-overlap]').should('contain', 'previous 30 days');
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

  it('uses no-team groups and the actual follow-up audience and combined denominator', () => {
    const followup = camp({
      id: 80, team_tags: [], followup_of: 70, followup_parent_sent: 2000,
      followup_condition: { open: 'any', click: 'any' }, combined_clickers: 120, combined_viewers: 550,
    });
    stub({ cur: [...mine, followup] });
    cy.loginAndVisit('/');
    cy.get('[data-cy=team-row-__none__]').click();
    cy.get('[data-cy=team-filter]').should('contain', 'No team');
    cy.get('[data-cy=card-age]').should('contain', '1,500 unique recipients');
    cy.get('[data-cy=card-country]').should('contain', '4,000 unique recipients');
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
    cy.loginAndVisit('/');
    cy.get('[data-cy=stats-summary]').should('contain', 'Previous period for the same team 1.0% (+8.0%p)');
    cy.get('[data-cy="team-row-팀:가"]').should('contain', '+8.0%p')
      .find('[data-cy=help-click]').trigger('mouseenter', { force: true });
    cy.get('[data-cy="team-row-팀:가"] [data-cy=help-click]')
      .should('contain', '270 ÷ 3,000 sent (3 campaigns)').and('contain', 'Vertical line').and('contain', 'Previous period · 1.0%');
  });

  it('distinguishes an unreadable previous period from absent sends and requests all time', () => {
    stub({ prev: null });
    cy.loginAndVisit('/');
    cy.get('[data-cy=stats-summary]').should('contain', 'Previous period results could not be loaded').and('not.contain', 'No sends');
    cy.intercept('GET', '/api/stats/campaigns*', (req) => {
      if (req.query.from === 'all') {
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
    cy.loginAndVisit('/');
    cy.get('[data-cy=team-comparison-start]').should('contain', '3 eligible campaigns and 1,000 sends');
    cy.get('[data-cy="team-row-팀:라"]').should('contain', 'Not tracked').and('not.contain', '0.0%');
    cy.get('[data-cy=camp-row-30]').should('contain', 'Not tracked');
  });

  it('shows the empty "no sends" rows when no campaign was sent', () => {
    stub({ cur: [] });
    cy.loginAndVisit('/');
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
    cy.loginAndVisit('/admin/campaigns');
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
    cy.loginAndVisit('/admin/campaigns/analytics?id=52');
    cy.get('[data-cy=result-click]').should('contain', 'Not tracked');
    cy.get('[data-cy=btn-proof-summary]').click();
    cy.get('[data-cy=proof-summary-text]').invoke('val')
      .should('contain', '발송 1,000 · 열람 추적 없음 · 클릭 추적 없음 · 반송 4');
  });

  it('hides empty charts and queries from the actual start for a recent campaign', () => {
    stub();
    cy.loginAndVisit('/admin/campaigns/analytics');
    cy.get('[data-cy=analytics-empty]').should('be.visible');
    cy.get('.charts').should('not.exist');
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

});

describe('Statistics against the server', () => {
  it('answers the statistics API for an administrator and keeps the dashboard counts', () => {
    cy.loginAndVisit('/');
    // The real queries read every campaign of the database the other specs filled: wait longer.
    cy.get('[data-cy=stats-teams]', { timeout: 30000 }).should('exist');
    cy.request('/api/stats/campaigns').its('body.data.campaigns').should('be.an', 'array');
    cy.request('/api/stats/recipients?by=country').its('body.data').should('be.an', 'array');
    cy.request({ url: '/api/stats/recipients?by=x', failOnStatusCode: false }).its('status').should('eq', 400);
    // The folded counts stay in the page.
    cy.get('[data-cy=lists] .title').should('exist');
    cy.get('[data-cy=counts-toggle]').click();
    cy.get('[data-cy=lists]').should('be.visible');
  });
});
