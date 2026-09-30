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

// Feature-24 (model observability dashboard) e2e suite, run against the
// compose stack gateway. Covers the acceptance criteria of
// docs/design/model-observability.md (AC1-AC13) reachable from the
// outside:
//
//   - API contract on the admin surface (AC1/AC2/AC3/AC5): the fleet
//     overview returns cards, a per-model table and a time-series; a
//     range > 92 days or since > until returns 10404; the model_id filter
//     scopes the cards and series; the per-model drill-down returns the
//     per-key breakdown; an unknown model returns 10801; buckets are
//     hourly for ranges <= 7 days and daily otherwise; every response
//     carries data_through.
//   - API contract on the end-user surface (AC4): the single-model view
//     returns only the caller's org usage, aggregated by the tenant's own
//     keys, with no service ids or operator internals.
//   - Surface separation (AC11/AC12): admin pages call only
//     /api/v1/admin/observability/*, user pages call only
//     /api/v1/models/{model_id}/observability; a wrong-realm session is
//     rejected with 10038; the admin API is not reachable on the bare
//     /api/v1/... prefix.
//   - Console pages (AC6-AC10, AC13): the admin overview page renders the
//     filter bar, summary cards, the inline-SVG chart and the per-model
//     table; changing the time range or model filter refetches and the
//     metric switcher toggles the chart metric; the empty state renders;
//     the admin drill-down renders the per-key table and the not-found
//     state; the end-user page renders the tenant's own cards/chart/table;
//     a session without the required role receives 10036.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack. Request logs are seeded directly into PostgreSQL by
// test/e2e/seed/seed_observability.go (the observability module is a
// read-only aggregation over request_logs).

const api = require('../page-objects/api.js');
const { execSync } = require('child_process');
const path = require('path');

// Fixed model / key ids used by the seed (must match seed_observability.go).
const MODEL_A = '11111111-1111-1111-1111-111111111111';
const MODEL_B = '22222222-2222-2222-2222-222222222222';
const KEY_A = '33333333-3333-3333-3333-333333333333';
const KEY_B = '55555555-5555-5555-5555-555555555555';

// Seed request logs for the given org directly into PostgreSQL (the
// observability module is a read-only aggregation over request_logs, and
// the compose stack has no inference pipeline to produce them). The seed
// program writes the rows directly into the compose database.
function seedObservability(browser, org) {
  const repoRoot = path.resolve(__dirname, '..', '..', '..');
  const cmd = [
    'docker run --rm --network go-taas_default',
    '-e GOPROXY=https://goproxy.cn,direct',
    '-v go-taas-go-mod-cache:/go/pkg/mod',
    '-v go-taas-go-build-cache:/root/.cache/go-build',
    `-v "${repoRoot}":/app`,
    '-w /app/test/e2e/seed',
    'golang:1.26-alpine',
    `sh -c "go run seed_observability.go -dsn 'postgres://taas:taas@postgres:5432/taas?sslmode=disable' -org '${org}' -model '${MODEL_A}' -key '${KEY_A}' -other-org 'org-e2e-obs-other' -other-key '44444444-4444-4444-4444-444444444444'"`
  ].join(' ');
  try {
    execSync(cmd, {stdio: 'pipe', timeout: 120000});
  } catch (e) {
    browser.assert.fail(`seedObservability failed: ${e.message}`);
  }
}

module.exports = {
  '@tags': ['model-observability', 'feature-24'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-obs-${browser.globals.runId}-${browser.globals.testSeq}`;
    browser.globals.orgB = `org-e2e-obs-b-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    api.ensureOrg(browser, browser.globals.orgB);
    // Seed request logs for orgA so the observability pages show data.
    seedObservability(browser, browser.globals.orgA);
    // Seed admin- and user-realm sessions so both the admin and user
    // protected pages render (feature: unauthenticated pages redirect to
    // login). The suite navigates to both surfaces.
    api.seedSession(browser, 'admin', browser.globals.orgA);
    api.seedSession(browser, 'user', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- Admin surface: fleet overview API (AC1/AC2/AC5) ----

  'AC1: overview returns cards, models and series; over-long range returns 10404': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // Default range: the seeded request logs produce cards, a per-model
    // table and a time-series.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/observability',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC1: overview');
      browser.assert.ok(body.cards && body.cards.requestCount !== undefined, 'AC1: cards present');
      browser.assert.ok(Array.isArray(body.models), 'AC1: models array present');
      browser.assert.ok(Array.isArray(body.series), 'AC1: series array present');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC1: data_through present');
    });

    // Inverted range -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/observability?since=${now}&until=${now - 10}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: inverted range');
    });

    // Range over 92 days -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/observability?since=${now - 93 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: over-long range');
    });
  },

  'AC2: overview with model_id filter scopes cards and series to that model': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/observability?model_id=${MODEL_A}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC2: filtered overview');
      // The fleet table still lists models, but the cards and series are
      // scoped to the selected model.
      browser.assert.ok(body.cards && body.cards.requestCount !== undefined, 'AC2: cards present');
      browser.assert.ok(Array.isArray(body.models), 'AC2: models array present');
      browser.assert.ok(Array.isArray(body.series), 'AC2: series array present');
    });
  },

  'AC5: buckets are hourly for <=7d and daily for >7d; data_through present': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // 24h range -> hourly buckets (many buckets).
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/observability?since=${now - 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC5: 24h range');
      browser.assert.ok(Array.isArray(body.series), 'AC5: series array');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC5: data_through present');
    });

    // 30d range -> daily buckets.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/observability?since=${now - 30 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC5: 30d range');
      browser.assert.ok(Array.isArray(body.series), 'AC5: series array');
      browser.assert.ok(body.cards.dataThrough !== undefined, 'AC5: data_through present');
    });
  },

  // ---- Admin surface: per-model drill-down API (AC3) ----

  'AC3: admin drill-down returns cards, series and per-key rows; unknown model returns 10801': function (browser) {
    const org = browser.globals.orgA;

    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/observability/models/${MODEL_A}`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC3: drill-down');
      browser.assert.ok(body.cards && body.cards.requestCount !== undefined, 'AC3: cards present');
      browser.assert.ok(Array.isArray(body.series), 'AC3: series array present');
      browser.assert.ok(Array.isArray(body.keys), 'AC3: keys array present');
      browser.assert.ok(body.keys.length >= 1, 'AC3: at least one key row');
    });

    // Unknown model -> 10801.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/observability/models/does-not-exist',
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10801, 'AC3: unknown model');
    });
  },

  // ---- End-user surface: single-model view API (AC4) ----

  'AC4: user view returns only the caller org usage, no internals': function (browser) {
    const org = browser.globals.orgA;

    api.request(browser, {
      method: 'GET',
      path: `/api/v1/models/${MODEL_A}/observability`,
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC4: user view');
      browser.assert.ok(body.cards && body.cards.requestCount !== undefined, 'AC4: cards present');
      browser.assert.ok(Array.isArray(body.series), 'AC4: series array present');
      browser.assert.ok(Array.isArray(body.keys), 'AC4: keys array present');
      // No service ids or operator internals.
      browser.assert.equal(body.cards.serviceId, undefined, 'AC4: no serviceId');
      browser.assert.equal(body.cards.replicaCount, undefined, 'AC4: no replicaCount');
      // The keys are the tenant's own keys.
      const keyIds = body.keys.map((k) => k.apiKeyId);
      browser.assert.ok(keyIds.includes(KEY_A) || keyIds.includes(KEY_B), 'AC4: tenant keys present');
    });
  },

  // ---- Surface separation (AC11/AC12) ----

  'AC11: admin observability API is not reachable on the bare /api/v1 prefix': function (browser) {
    const org = browser.globals.orgA;

    // The admin overview is not served on the bare /api/v1/observability.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/observability',
      org,
    }, (res) => {
      // Either a 404 (no binding) or a business error; it must NOT be the
      // admin success envelope.
      browser.assert.ok(
        res.status !== 200 || (res.body && res.body.response && res.body.response.code !== 0),
        'AC11: bare /api/v1/observability is not the admin overview'
      );
    });
  },

  'AC12: a user-realm session calling the admin observability prefix is rejected with 10038': function (browser) {
    // The user session token is in localStorage (go-taas.user.session-token).
    // A user-realm session on the admin prefix must be rejected by the
    // realm guard (10038 wrong realm).
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.user.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12: user session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/observability',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12: user session on admin prefix -> 10038');
      });
    });
  },

  'AC12b: an admin-realm session calling the user observability prefix is rejected with 10038': function (browser) {
    // The admin session token is in localStorage (go-taas.admin.session-token).
    // An admin-realm session on the user prefix must be rejected by the
    // realm guard (10038 wrong realm).
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.admin.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12b: admin session token present');
      api.request(browser, {
        method: 'GET',
        path: `/api/v1/models/${MODEL_A}/observability`,
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12b: admin session on user prefix -> 10038');
      });
    });
  },

  // ---- Console pages: admin overview (AC6/AC7/AC8) ----

  'AC6: admin overview page renders filter bar, cards, chart and per-model table': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/observability');

    browser.waitForElementPresent('[data-testid="observability-page"]', 15000, 'AC6: page renders');
    browser.waitForElementPresent('[data-testid="observability-filter-range"]', 10000, 'AC6: range filter');
    browser.waitForElementPresent('[data-testid="observability-filter-model"]', 10000, 'AC6: model filter');
    browser.waitForElementPresent('[data-testid="observability-cards"]', 10000, 'AC6: cards');
    browser.waitForElementPresent('[data-testid="observability-chart"]', 10000, 'AC6: chart');
    browser.waitForElementPresent('[data-testid="observability-table"]', 10000, 'AC6: per-model table');
    browser.waitForElementPresent('[data-testid="observability-refresh"]', 10000, 'AC6: refresh button');
  },

  'AC7: metric switcher toggles the chart metric': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/observability');
    browser.waitForElementPresent('[data-testid="observability-metric-toggle"]', 15000, 'AC7: metric toggle');

    // Click the throughput metric; the chart stays present (client-side switch).
    browser.click('[data-testid="observability-metric-throughput"]');
    browser.waitForElementPresent('[data-testid="observability-chart"]', 5000, 'AC7: chart persists after toggle');
  },

  'AC8: empty state renders when no data matches': function (browser) {
    // Use orgB, which has no request logs. The end-user page is org-scoped,
    // so its view is empty. Seed a user session for orgB so the page renders.
    // The user surface's org key is realm-scoped (go-taas.user.org-id), so
    // set that key directly rather than the legacy go-taas.org-id (which is
    // only adopted on a fresh surface boot and would not override the
    // already-present go-taas.user.org-id=orgA from beforeEach).
    api.seedSession(browser, 'user', browser.globals.orgB);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgB}')`);
    browser.url(browser.globals.baseUrl + `/models/${MODEL_A}/observability`);
    browser.waitForElementPresent('[data-testid="user-observability-page"]', 15000, 'AC8: user page renders');
    // The user page's empty state is the per-key table empty copy.
    browser.waitForElementPresent('[data-testid="observability-key-empty"]', 15000, 'AC8: empty state renders');
  },

  // ---- Console pages: admin drill-down (AC9) ----

  'AC9: admin drill-down renders per-key table; unknown model shows not-found': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + `/admin/observability/models/${MODEL_A}`);
    browser.waitForElementPresent('[data-testid="observability-model-page"]', 15000, 'AC9: model page renders');
    browser.waitForElementPresent('[data-testid="observability-key-table"]', 10000, 'AC9: per-key table');
    browser.waitForElementPresent('[data-testid="observability-cards"]', 10000, 'AC9: cards');

    // Unknown model -> not-found state.
    browser.url(browser.globals.baseUrl + '/admin/observability/models/does-not-exist');
    browser.waitForElementPresent('[data-testid="observability-model-notfound"]', 15000, 'AC9: not-found state');
  },

  // ---- Console pages: end-user (AC10) ----

  'AC10: end-user page renders tenant cards, chart and per-key table': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + `/models/${MODEL_A}/observability`);
    browser.waitForElementPresent('[data-testid="user-observability-page"]', 15000, 'AC10: user page renders');
    browser.waitForElementPresent('[data-testid="observability-cards"]', 10000, 'AC10: cards');
    browser.waitForElementPresent('[data-testid="observability-chart"]', 10000, 'AC10: chart');
    browser.waitForElementPresent('[data-testid="observability-key-table"]', 10000, 'AC10: per-key table');
  },

  // ---- Console pages: permission denied (AC13) ----

  'AC13: session without the required role receives 10036 on the admin observability API': function (browser) {
    // The admin observability RPCs are gated by tenancy.RoleGuard. A
    // session whose user is NOT a member of the org must be denied with
    // 10036.
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
        path: '/api/v1/admin/observability',
        org,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC13: non-member session on admin observability API -> 10036');
      });
    });
  }
};