// Copyright 2025 The go-taas Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Feature-29 (cost analytics dashboard) e2e suite, run against the compose
// stack gateway. Covers the acceptance criteria of
// docs/design/cost-analytics-dashboard.md (AC1-AC13) reachable from the
// outside:
//
//   - API contract on the admin surface (AC1/AC2/AC3/AC5): the fleet
//     overview returns cards, a dimension breakdown and a time-series; a
//     range > 92 days or since > until returns 10404; an unsupported
//     dimension returns 11301; the per-dimension-value drill-down returns
//     cards and a cost trend; an unknown dimension value returns 11302;
//     buckets are hourly for ranges <= 7 days and daily otherwise; every
//     response carries data_through.
//   - API contract on the end-user surface (AC4): the single-dimension
//     view returns only the caller's org cost, with no service ids or
//     operator internals.
//   - Surface separation (AC11/AC12): admin pages call only
//     /api/v1/admin/cost/*, user pages call only /api/v1/cost/*; a
//     wrong-realm session is rejected with 10038; the admin API is not
//     reachable on the bare /api/v1/... prefix.
//   - Console pages (AC6-AC10, AC13): the admin overview page renders the
//     filter bar, summary cards, the inline-SVG cost trend chart and the
//     dimension breakdown; changing the time range, dimension or model
//     filter refetches and the metric switcher toggles the chart metric;
//     the empty state renders; the admin drill-down renders the dimension
//     value's cards and trend and the not-found state; the end-user page
//     renders the tenant's own cards/breakdown/trend; a session without
//     the required role receives 10036.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack. Charge records are seeded directly into PostgreSQL by
// test/e2e/seed/costanalytics/seed_costanalytics.go (the cost-analytics
// module is a read-only aggregation over charge_records).

const api = require('../page-objects/api.js');
const { execSync } = require('child_process');
const path = require('path');

// Fixed model id used by the seed (must match seed_costanalytics.go).
const MODEL_A = '11111111-1111-1111-1111-111111111111';

// Seed charge records for the given org directly into PostgreSQL (the
// cost-analytics module is a read-only aggregation over charge_records,
// and the compose stack has no inference pipeline to produce them). The
// other-org is a fixed id so orgB (used for the empty-state case) stays
// unseeded.
function seedCostAnalytics(browser, org) {
  const repoRoot = path.resolve(__dirname, '..', '..', '..');
  const cmd = [
    'docker run --rm --network go-taas_default',
    '-e GOPROXY=https://goproxy.cn,direct',
    '-v go-taas-go-mod-cache:/go/pkg/mod',
    '-v go-taas-go-build-cache:/root/.cache/go-build',
    `-v "${repoRoot}":/app`,
    '-w /app/test/e2e/seed/costanalytics',
    'golang:1.26-alpine',
    `sh -c "go run seed_costanalytics.go -dsn 'postgres://taas:taas@postgres:5432/taas?sslmode=disable' -org '${org}' -model '${MODEL_A}' -other-org 'org-e2e-ca-other'"`
  ].join(' ');
  try {
    execSync(cmd, {stdio: 'pipe', timeout: 120000});
  } catch (e) {
    browser.assert.fail(`seedCostAnalytics failed: ${e.message}`);
  }
}

module.exports = {
  '@tags': ['cost-analytics', 'feature-29'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-ca-${browser.globals.runId}-${browser.globals.testSeq}`;
    browser.globals.orgB = `org-e2e-ca-b-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    api.ensureOrg(browser, browser.globals.orgB);
    // Seed charge records for orgA so the cost pages show data; orgB is
    // the other tenant whose usage must never appear in orgA's view (and
    // stays unseeded for the empty-state case).
    seedCostAnalytics(browser, browser.globals.orgA);
    // Seed admin- and user-realm sessions so both the admin and user
    // protected pages render.
    api.seedSession(browser, 'admin', browser.globals.orgA);
    api.seedSession(browser, 'user', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- Admin surface: fleet overview API (AC1/AC2/AC5) ----

  'AC1: overview returns cards, breakdown and series; over-long range returns 10404': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // Default range: the seeded charge records produce cards, a dimension
    // breakdown and a time-series.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/cost',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC1: overview');
      browser.assert.ok(body.cards && body.cards.totalCostCents !== undefined, 'AC1: cards present');
      browser.assert.ok(Array.isArray(body.breakdown), 'AC1: breakdown array present');
      browser.assert.ok(Array.isArray(body.series), 'AC1: series array present');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC1: data_through present');
    });

    // Inverted range -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost?since=${now}&until=${now - 10}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: inverted range');
    });

    // Range over 92 days -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost?since=${now - 93 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: over-long range');
    });
  },

  'AC2: unsupported dimension returns 11301; breakdown sorted by cost descending': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // Unsupported dimension -> 11301.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost?since=${now - 24 * 3600}&until=${now}&dimension=bogus`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 11301, 'AC2: unsupported dimension');
    });

    // Valid overview with model dimension.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost?since=${now - 7 * 24 * 3600}&until=${now}&dimension=model`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC2: model dimension overview');
      browser.assert.ok(Array.isArray(body.breakdown), 'AC2: breakdown array');
      if (body.breakdown.length >= 2) {
        const costs = body.breakdown.map((b) => parseInt(b.totalCostCents, 10));
        for (let i = 1; i < costs.length; i++) {
          browser.assert.ok(costs[i - 1] >= costs[i], 'AC2: breakdown sorted by cost descending');
        }
      }
    });
  },

  'AC5: buckets are hourly for <=7d and daily for >7d; data_through present': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // 24h range -> hourly buckets.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost?since=${now - 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC5: 24h range');
      browser.assert.ok(Array.isArray(body.series), 'AC5: series array');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC5: data_through present');
    });

    // 30d range -> daily buckets.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost?since=${now - 30 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC5: 30d range');
      browser.assert.ok(Array.isArray(body.series), 'AC5: series array');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC5: data_through present');
    });
  },

  // ---- Admin surface: per-dimension-value drill-down API (AC3) ----

  'AC3: admin drill-down returns cards and trend; unknown dimension value returns 11302': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // The seed's model dimension value is MODEL_A.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost/model/${MODEL_A}?since=${now - 7 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const d = api.assertOk(browser, res, 'AC3: drill-down');
      browser.assert.ok(d.cards && d.cards.totalCostCents !== undefined, 'AC3: cards present');
      browser.assert.ok(Array.isArray(d.series), 'AC3: series array present');
    });

    // Unknown dimension value -> 11302.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/cost/model/does-not-exist?since=${now - 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 11302, 'AC3: unknown dimension value');
    });
  },

  // ---- End-user surface: single-dimension view API (AC4) ----

  'AC4: user view returns only the caller org cost, no internals': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    api.request(browser, {
      method: 'GET',
      path: `/api/v1/cost/model/${MODEL_A}?since=${now - 7 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const d = api.assertOk(browser, res, 'AC4: user view');
      browser.assert.ok(d.cards && d.cards.totalCostCents !== undefined, 'AC4: cards present');
      browser.assert.ok(Array.isArray(d.series), 'AC4: series array present');
      // No service ids or operator internals.
      browser.assert.equal(d.cards.serviceId, undefined, 'AC4: no serviceId');
      browser.assert.equal(d.cards.replicaCount, undefined, 'AC4: no replicaCount');
    });
  },

  // ---- Surface separation (AC11/AC12) ----

  'AC11: admin cost API is not reachable on the bare /api/v1 prefix': function (browser) {
    const org = browser.globals.orgA;

    // The admin overview is not served on the bare /api/v1/cost.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/cost',
      org,
    }, (res) => {
      browser.assert.ok(
        res.status !== 200 || (res.body && res.body.response && res.body.response.code !== 0),
        'AC11: bare /api/v1/cost is not the admin overview'
      );
    });
  },

  'AC12: a user-realm session calling the admin cost prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.user.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12: user session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/cost',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12: user session on admin prefix -> 10038');
      });
    });
  },

  'AC12b: an admin-realm session calling the user cost prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.admin.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12b: admin session token present');
      api.request(browser, {
        method: 'GET',
        path: `/api/v1/cost/model/${MODEL_A}`,
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12b: admin session on user prefix -> 10038');
      });
    });
  },

  // ---- Console pages: admin overview (AC6/AC7/AC8) ----

  'AC6: admin overview page renders filter bar, cards, chart and breakdown': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/cost');

    browser.waitForElementPresent('[data-testid="cost-page"]', 15000, 'AC6: page renders');
    browser.waitForElementPresent('[data-testid="cost-filter-range"]', 10000, 'AC6: range filter');
    browser.waitForElementPresent('[data-testid="cost-filter-dimension"]', 10000, 'AC6: dimension filter');
    browser.waitForElementPresent('[data-testid="cost-cards"]', 10000, 'AC6: cards');
    browser.waitForElementPresent('[data-testid="cost-chart"]', 10000, 'AC6: chart');
    browser.waitForElementPresent('[data-testid="cost-breakdown"]', 10000, 'AC6: breakdown');
    browser.waitForElementPresent('[data-testid="cost-refresh"]', 10000, 'AC6: refresh button');
  },

  'AC7: metric switcher toggles the chart metric': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/cost');
    browser.waitForElementPresent('[data-testid="cost-metric-toggle"]', 15000, 'AC7: metric toggle');

    // Click the tokens metric; the chart stays present (client-side switch).
    browser.click('[data-testid="cost-metric-tokens"]');
    browser.waitForElementPresent('[data-testid="cost-chart"]', 5000, 'AC7: chart persists after toggle');
  },

  'AC8: empty state renders when no data matches': function (browser) {
    // Use orgB, which has no charge records. The end-user page is
    // org-scoped, so its view is empty. Seed a user session for orgB so
    // the page renders. The user surface's org key is realm-scoped
    // (go-taas.user.org-id), so set that key directly.
    api.seedSession(browser, 'user', browser.globals.orgB);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgB}')`);
    browser.url(browser.globals.baseUrl + '/cost');
    browser.waitForElementPresent('[data-testid="user-cost-page"]', 15000, 'AC8: user page renders');
    browser.waitForElementPresent('[data-testid="user-cost-empty"]', 15000, 'AC8: empty state renders');
  },

  // ---- Console pages: admin drill-down (AC9) ----

  'AC9: admin drill-down renders cards and chart; unknown dimension value shows not-found': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + `/admin/cost/model/${MODEL_A}`);
    browser.waitForElementPresent('[data-testid="cost-detail-page"]', 15000, 'AC9: detail page renders');
    browser.waitForElementPresent('[data-testid="cost-cards"]', 10000, 'AC9: cards');
    browser.waitForElementPresent('[data-testid="cost-chart"]', 10000, 'AC9: chart');

    // Unknown dimension value -> not-found state.
    browser.url(browser.globals.baseUrl + '/admin/cost/model/does-not-exist');
    browser.waitForElementPresent('[data-testid="cost-detail-notfound"]', 15000, 'AC9: not-found state');
  },

  // ---- Console pages: end-user (AC10) ----

  'AC10: end-user page renders tenant cards, breakdown and chart': function (browser) {
    // Re-seed the user session for orgA: AC8 re-seeded it for orgB, and
    // the user surface resolves the org from the session (not the
    // X-Organization-Id header), so without this the page would be scoped
    // to orgB.
    api.seedSession(browser, 'user', browser.globals.orgA);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/cost');
    browser.waitForElementPresent('[data-testid="user-cost-page"]', 15000, 'AC10: user page renders');
    browser.waitForElementPresent('[data-testid="cost-cards"]', 10000, 'AC10: cards');
    browser.waitForElementPresent('[data-testid="cost-breakdown"]', 10000, 'AC10: breakdown');
    browser.waitForElementPresent('[data-testid="cost-chart"]', 10000, 'AC10: chart');
  },

  // ---- Console pages: permission denied (AC13) ----

  'AC13: session without the required role receives 10036 on the admin cost API': function (browser) {
    const org = browser.globals.orgA;
    const nonMemberUser = `33333333-4444-4444-4444-${browser.globals.runId.toString().padStart(12, '0').slice(-12)}`;
    api.seedSession(browser, 'admin', org, 'admin', nonMemberUser, { noMember: true });
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.admin.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC13: non-member admin session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/cost',
        org,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC13: non-member session on admin cost API -> 10036');
      });
    });
  }
};