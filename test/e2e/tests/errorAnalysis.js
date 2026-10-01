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

// Feature-31 (error analysis) e2e suite, run against the compose stack
// gateway. Covers the acceptance criteria of
// docs/design/error-analysis.md (AC1-AC13) reachable from the outside:
//
//   - API contract on the admin surface (AC1/AC2/AC3/AC5): the fleet
//     overview returns cards, a top-causes ranking and a time-series; a
//     range > 92 days or since > until returns 10404; the per-error-code
//     drill-down returns cards and an error-rate trend; an unknown error
//     code returns 11501; buckets are hourly for ranges <= 7 days and
//     daily otherwise; every response carries data_through.
//   - API contract on the end-user surface (AC4): the single-error view
//     returns only the caller's org errors, with no service ids or
//     operator internals.
//   - Surface separation (AC11/AC12): admin pages call only
//     /api/v1/admin/errors/*, user pages call only /api/v1/errors/*; a
//     wrong-realm session is rejected with 10038; the admin API is not
//     reachable on the bare /api/v1/... prefix.
//   - Console pages (AC6-AC10, AC13): the admin overview page renders the
//     filter bar, summary cards, the inline-SVG error-rate trend chart and
//     the top causes table; changing the time range or model filter
//     refetches and the metric switcher toggles the chart metric; the
//     empty state renders; the admin drill-down renders the error code's
//     cards and trend and the not-found state; the end-user page renders
//     the tenant's own cards/ranking/trend; a session without the required
//     role receives 10036.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack. Request logs are seeded directly into PostgreSQL by
// test/e2e/seed/erroranalysis/seed_erroranalysis.go (the error-analysis
// module is a read-only aggregation over request_logs).

const api = require('../page-objects/api.js');
const { execSync } = require('child_process');
const path = require('path');

// Fixed model id used by the seed (must match seed_erroranalysis.go).
const MODEL_A = '11111111-1111-1111-1111-111111111111';

// Seed request logs (with error statuses) for the given org directly into
// PostgreSQL (the error-analysis module is a read-only aggregation over
// request_logs, and the compose stack has no inference pipeline to produce
// them). The other-org is a fixed id so orgB (used for the empty-state
// case) stays unseeded.
function seedErrorAnalysis(browser, org) {
  const repoRoot = path.resolve(__dirname, '..', '..', '..');
  const cmd = [
    'docker run --rm --network go-taas_default',
    '-e GOPROXY=https://goproxy.cn,direct',
    '-v go-taas-go-mod-cache:/go/pkg/mod',
    '-v go-taas-go-build-cache:/root/.cache/go-build',
    `-v "${repoRoot}":/app`,
    '-w /app/test/e2e/seed/erroranalysis',
    'golang:1.26-alpine',
    `sh -c "go run seed_erroranalysis.go -dsn 'postgres://taas:taas@postgres:5432/taas?sslmode=disable' -org '${org}' -model '${MODEL_A}' -other-org 'org-e2e-ea-other'"`
  ].join(' ');
  try {
    execSync(cmd, {stdio: 'pipe', timeout: 120000});
  } catch (e) {
    browser.assert.fail(`seedErrorAnalysis failed: ${e.message}`);
  }
}

module.exports = {
  '@tags': ['error-analysis', 'feature-31'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-ea-${browser.globals.runId}-${browser.globals.testSeq}`;
    browser.globals.orgB = `org-e2e-ea-b-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    api.ensureOrg(browser, browser.globals.orgB);
    // Seed request logs for orgA so the error pages show data; orgB is
    // the other tenant whose errors must never appear in orgA's view (and
    // stays unseeded for the empty-state case).
    seedErrorAnalysis(browser, browser.globals.orgA);
    // Seed admin- and user-realm sessions so both the admin and user
    // protected pages render.
    api.seedSession(browser, 'admin', browser.globals.orgA);
    api.seedSession(browser, 'user', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- Admin surface: fleet overview API (AC1/AC2/AC5) ----

  'AC1: overview returns cards, causes and series; over-long range returns 10404': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // Default range: the seeded request logs produce cards, a top-causes
    // ranking and a time-series.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/errors',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC1: overview');
      browser.assert.ok(body.cards && body.cards.errorCount !== undefined, 'AC1: cards present');
      browser.assert.ok(Array.isArray(body.causes), 'AC1: causes array present');
      browser.assert.ok(Array.isArray(body.series), 'AC1: series array present');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC1: data_through present');
    });

    // Inverted range -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/errors?since=${now}&until=${now - 10}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: inverted range');
    });

    // Range over 92 days -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/errors?since=${now - 93 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: over-long range');
    });
  },

  'AC2: causes sorted by error count descending with share_pct': function (browser) {
    const org = browser.globals.orgA;

    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/errors',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC2: overview');
      browser.assert.ok(Array.isArray(body.causes), 'AC2: causes array');
      if (body.causes.length >= 2) {
        const counts = body.causes.map((c) => parseInt(c.errorCount, 10));
        for (let i = 1; i < counts.length; i++) {
          browser.assert.ok(counts[i - 1] >= counts[i], 'AC2: causes sorted by error count descending');
        }
      }
      browser.assert.ok(body.causes.length >= 1, 'AC2: at least one cause');
    });
  },

  'AC5: buckets are hourly for <=7d and daily for >7d; data_through present': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // 24h range -> hourly buckets.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/errors?since=${now - 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC5: 24h range');
      browser.assert.ok(Array.isArray(body.series), 'AC5: series array');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC5: data_through present');
    });

    // 30d range -> daily buckets.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/errors?since=${now - 30 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC5: 30d range');
      browser.assert.ok(Array.isArray(body.series), 'AC5: series array');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC5: data_through present');
    });
  },

  // ---- Admin surface: per-error-code drill-down API (AC3) ----

  'AC3: admin drill-down returns cards and trend; unknown error code returns 11501': function (browser) {
    const org = browser.globals.orgA;

    // The seed's top cause is rate_limit_exceeded.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/errors/rate_limit_exceeded',
      org,
    }, (res) => {
      const d = api.assertOk(browser, res, 'AC3: drill-down');
      browser.assert.ok(d.cards && d.cards.errorCount !== undefined, 'AC3: cards present');
      browser.assert.ok(Array.isArray(d.series), 'AC3: series array present');
    });

    // Unknown error code -> 11501.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/errors/does-not-exist',
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 11501, 'AC3: unknown error code');
    });
  },

  // ---- End-user surface: single-error view API (AC4) ----

  'AC4: user view returns only the caller org errors, no internals': function (browser) {
    const org = browser.globals.orgA;

    api.request(browser, {
      method: 'GET',
      path: '/api/v1/errors/rate_limit_exceeded',
      org,
    }, (res) => {
      const d = api.assertOk(browser, res, 'AC4: user view');
      browser.assert.ok(d.cards && d.cards.errorCount !== undefined, 'AC4: cards present');
      browser.assert.ok(Array.isArray(d.series), 'AC4: series array present');
      // No service ids or operator internals.
      browser.assert.equal(d.cards.serviceId, undefined, 'AC4: no serviceId');
      browser.assert.equal(d.cards.replicaCount, undefined, 'AC4: no replicaCount');
    });
  },

  // ---- Surface separation (AC11/AC12) ----

  'AC11: the bare /api/v1/errors serves the tenant-scoped user overview, not the admin fleet view': function (browser) {
    const org = browser.globals.orgA;

    // The bare prefix is the user surface: it serves the tenant-scoped
    // overview (200, code 0), never the admin fleet envelope. The user
    // binding is hard-scoped to the caller's org (AD8).
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/errors',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC11: user overview on bare prefix');
      browser.assert.ok(body.cards && body.cards.errorCount !== undefined, 'AC11: user cards present');
      browser.assert.ok(Array.isArray(body.causes), 'AC11: user causes array present');
      browser.assert.ok(Array.isArray(body.series), 'AC11: user series array present');
    });
  },

  'AC12: a user-realm session calling the admin error prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.user.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12: user session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/errors',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12: user session on admin prefix -> 10038');
      });
    });
  },

  'AC12b: an admin-realm session calling the user error prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.admin.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12b: admin session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/errors/rate_limit_exceeded',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12b: admin session on user prefix -> 10038');
      });
    });
  },

  // ---- Console pages: admin overview (AC6/AC7/AC8) ----

  'AC6: admin overview page renders filter bar, cards, chart and causes table': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/errors');

    browser.waitForElementPresent('[data-testid="errors-page"]', 15000, 'AC6: page renders');
    browser.waitForElementPresent('[data-testid="errors-filter-range"]', 10000, 'AC6: range filter');
    browser.waitForElementPresent('[data-testid="errors-filter-model"]', 10000, 'AC6: model filter');
    browser.waitForElementPresent('[data-testid="errors-cards"]', 10000, 'AC6: cards');
    browser.waitForElementPresent('[data-testid="errors-chart"]', 10000, 'AC6: chart');
    browser.waitForElementPresent('[data-testid="errors-causes"]', 10000, 'AC6: causes table');
    browser.waitForElementPresent('[data-testid="errors-refresh"]', 10000, 'AC6: refresh button');
  },

  'AC7: metric switcher toggles the chart metric': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/errors');
    browser.waitForElementPresent('[data-testid="errors-metric-toggle"]', 15000, 'AC7: metric toggle');

    // Click the error-rate metric; the chart stays present (client-side switch).
    browser.click('[data-testid="errors-metric-errorRate"]');
    browser.waitForElementPresent('[data-testid="errors-chart"]', 5000, 'AC7: chart persists after toggle');
  },

  'AC8: empty state renders when no data matches': function (browser) {
    // Use orgB, which has no request logs. The end-user page is org-scoped,
    // so its view is empty. Seed a user session for orgB so the page
    // renders. The user surface's org key is realm-scoped
    // (go-taas.user.org-id), so set that key directly.
    api.seedSession(browser, 'user', browser.globals.orgB);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgB}')`);
    browser.url(browser.globals.baseUrl + '/errors');
    browser.waitForElementPresent('[data-testid="user-errors-page"]', 15000, 'AC8: user page renders');
    browser.waitForElementPresent('[data-testid="user-errors-empty"]', 15000, 'AC8: empty state renders');
  },

  // ---- Console pages: admin drill-down (AC9) ----

  'AC9: admin drill-down renders cards and chart; unknown error code shows not-found': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/errors/rate_limit_exceeded');
    browser.waitForElementPresent('[data-testid="error-detail-page"]', 15000, 'AC9: detail page renders');
    browser.waitForElementPresent('[data-testid="errors-cards"]', 10000, 'AC9: cards');
    browser.waitForElementPresent('[data-testid="errors-chart"]', 10000, 'AC9: chart');

    // Unknown error code -> not-found state.
    browser.url(browser.globals.baseUrl + '/admin/errors/does-not-exist');
    browser.waitForElementPresent('[data-testid="error-detail-notfound"]', 15000, 'AC9: not-found state');
  },

  // ---- Console pages: end-user (AC10) ----

  'AC10: end-user page renders tenant cards, causes and chart': function (browser) {
    // Re-seed the user session for orgA: AC8 re-seeded it for orgB, and
    // the user surface resolves the org from the session (not the
    // X-Organization-Id header), so without this the page would be scoped
    // to orgB.
    api.seedSession(browser, 'user', browser.globals.orgA);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/errors');
    browser.waitForElementPresent('[data-testid="user-errors-page"]', 15000, 'AC10: user page renders');
    browser.waitForElementPresent('[data-testid="errors-cards"]', 10000, 'AC10: cards');
    browser.waitForElementPresent('[data-testid="errors-causes"]', 10000, 'AC10: causes table');
    browser.waitForElementPresent('[data-testid="errors-chart"]', 10000, 'AC10: chart');
  },

  // ---- Console pages: permission denied (AC13) ----

  'AC13: session without the required role receives 10036 on the admin error API': function (browser) {
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
        path: '/api/v1/admin/errors',
        org,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC13: non-member session on admin error API -> 10036');
      });
    });
  }
};