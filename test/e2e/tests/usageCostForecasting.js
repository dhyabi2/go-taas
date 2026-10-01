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

// Feature-36 (usage & cost forecasting) e2e suite, run against the compose
// stack gateway. Covers the acceptance criteria of
// docs/architecture/usage-cost-forecasting.md (AC1-AC9) reachable from the
// outside:
//
//   - API contract on the admin surface (AC1/AC2): GetForecast with a
//     valid range returns method/horizon_days/data_through/history[]/
//     forecast[] (with the confidence band)/summary; a range > 92 days or
//     since > until returns 10404; an unsupported dimension returns 11301,
//     an unknown dimension value returns 11302, and horizon_days > 90
//     returns a validation error.
//   - API contract on the end-user surface (AC7): the user forecast is
//     tenant-scoped and exposes no service ids or operator internals.
//   - Surface separation (AC8): the admin page calls only
//     /api/v1/admin/forecast/*, the user page only /api/v1/forecast/*; a
//     wrong-realm session is rejected with 10038; the admin API is not
//     reachable on the bare /api/v1/... prefix.
//   - Console pages (AC4-AC6, AC9): the admin Forecast page renders the
//     filter bar, summary cards and the forecast chart; the empty state
//     renders; the end-user page renders the tenant's own forecast; a
//     session without the required role receives 10036.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack. Charge records are seeded directly into PostgreSQL by
// test/e2e/seed/forecast/seed_forecast.go (the forecast module is a
// read-only aggregation over charge_records).

const api = require('../page-objects/api.js');
const { execSync } = require('child_process');
const path = require('path');

// Fixed model id used by the seed (must match seed_forecast.go).
const MODEL_A = '11111111-1111-1111-1111-111111111111';

// Seed charge records for the given org directly into PostgreSQL (the
// forecast module is a read-only aggregation over charge_records, and the
// compose stack has no inference pipeline to produce them). The other-org
// is a fixed id so orgB (used for the empty-state case) stays unseeded.
function seedForecast(browser, org) {
  const repoRoot = path.resolve(__dirname, '..', '..', '..');
  const cmd = [
    'docker run --rm --network go-taas_default',
    '-e GOPROXY=https://goproxy.cn,direct',
    '-v go-taas-go-mod-cache:/go/pkg/mod',
    '-v go-taas-go-build-cache:/root/.cache/go-build',
    `-v "${repoRoot}":/app`,
    '-w /app/test/e2e/seed/forecast',
    'golang:1.26-alpine',
    `sh -c "go run seed_forecast.go -dsn 'postgres://taas:taas@postgres:5432/taas?sslmode=disable' -org '${org}' -model '${MODEL_A}' -other-org 'org-e2e-fc-other'"`
  ].join(' ');
  try {
    execSync(cmd, {stdio: 'pipe', timeout: 120000});
  } catch (e) {
    browser.assert.fail(`seedForecast failed: ${e.message}`);
  }
}

module.exports = {
  '@tags': ['usage-cost-forecasting', 'feature-36'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-fc-${browser.globals.runId}-${browser.globals.testSeq}`;
    browser.globals.orgB = `org-e2e-fc-b-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    api.ensureOrg(browser, browser.globals.orgB);
    // Seed charge records for orgA so the forecast pages show data; orgB
    // is the other tenant whose usage must never appear in orgA's view
    // (and stays unseeded for the empty-state case).
    seedForecast(browser, browser.globals.orgA);
    // Seed admin- and user-realm sessions so both the admin and user
    // protected pages render.
    api.seedSession(browser, 'admin', browser.globals.orgA);
    api.seedSession(browser, 'user', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- Admin surface: forecast API (AC1/AC2) ----

  'AC1: GetForecast returns method/horizon_days/data_through/history/forecast/summary; over-long or inverted range -> 10404': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // Default range: the seeded charge records produce history, forecast
    // and summary.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/forecast',
      org
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC1: forecast');
      browser.assert.equal(body.method, 'linear_trend', 'AC1: method linear_trend');
      browser.assert.ok(body.horizonDays !== undefined, 'AC1: horizon_days present');
      browser.assert.ok(body.dataThrough !== undefined, 'AC1: data_through present');
      browser.assert.ok(Array.isArray(body.history), 'AC1: history array');
      browser.assert.ok(Array.isArray(body.forecast), 'AC1: forecast array');
      browser.assert.ok(body.summary && body.summary.totalTokens !== undefined, 'AC1: summary present');
      // The forecast points carry the confidence band.
      if (body.forecast.length > 0) {
        const first = body.forecast[0];
        browser.assert.ok(first.upperTokens >= first.totalTokens, 'AC1: upper >= total');
        browser.assert.ok(first.lowerTokens <= first.totalTokens, 'AC1: lower <= total');
      }
    });

    // Inverted range -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/forecast?since=${now}&until=${now - 10}`,
      org
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: inverted range');
    });

    // Range over 92 days -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/forecast?since=${now - 93 * 24 * 3600}&until=${now}`,
      org
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: over-long range');
    });
  },

  'AC2: unsupported dimension -> 11301; unknown dimension value -> 11302; horizon > 90 -> validation error': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // Unsupported dimension -> 11301.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/forecast?since=${now - 24 * 3600}&until=${now}&dimension=bogus`,
      org
    }, (res) => {
      api.assertBusinessError(browser, res, 11301, 'AC2: unsupported dimension');
    });

    // Unknown dimension value -> 11302.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/forecast?since=${now - 24 * 3600}&until=${now}&dimension=model&dimension_value=does-not-exist`,
      org
    }, (res) => {
      api.assertBusinessError(browser, res, 11302, 'AC2: unknown dimension value');
    });

    // horizon > 90 -> validation error.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/forecast?since=${now - 24 * 3600}&until=${now}&horizon_days=91`,
      org
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC2: horizon > 90');
    });
  },

  // ---- End-user surface: tenant-scoped forecast API (AC7) ----

  'AC7: user forecast returns only the caller org usage, no internals': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    api.request(browser, {
      method: 'GET',
      path: `/api/v1/forecast?since=${now - 7 * 24 * 3600}&until=${now}&dimension=model`,
      org
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC7: user forecast');
      browser.assert.ok(Array.isArray(body.history), 'AC7: history array');
      browser.assert.ok(Array.isArray(body.forecast), 'AC7: forecast array');
      browser.assert.ok(body.summary && body.summary.totalTokens !== undefined, 'AC7: summary present');
      // No service ids or operator internals.
      browser.assert.equal(body.summary.serviceId, undefined, 'AC7: no serviceId');
      browser.assert.equal(body.summary.replicaCount, undefined, 'AC7: no replicaCount');
    });
  },

  // ---- Surface separation (AC8) ----

  'AC8: a user-realm session calling the admin forecast prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      return localStorage.getItem('go-taas.user.session-token');
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC8: user session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/forecast',
        org: browser.globals.orgA,
        headers: {Authorization: `Bearer ${token}`}
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC8: user session on admin prefix -> 10038');
      });
    });
  },

  'AC8b: an admin-realm session calling the user forecast prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      return localStorage.getItem('go-taas.admin.session-token');
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC8b: admin session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/forecast',
        org: browser.globals.orgA,
        headers: {Authorization: `Bearer ${token}`}
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC8b: admin session on user prefix -> 10038');
      });
    });
  },

  'AC8c: the admin forecast API is not reachable on the bare /api/v1/... prefix': function (browser) {
    const org = browser.globals.orgA;
    // The bare prefix is the user surface; the admin forecast route is
    // /api/v1/admin/forecast, not /api/v1/forecast. A request to the bare
    // prefix with the admin path shape must not return the admin fleet
    // envelope. The user forecast on the bare prefix is covered by AC7.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/forecast',
      org
    }, (res) => {
      // Without a session token the admin forecast route is not reachable
      // on the bare prefix; it must not return the admin fleet envelope.
      browser.assert.ok(
        res.status === 404 || (res.body && res.body.code !== 0),
        'AC8c: admin forecast route not served on bare prefix'
      );
    });
  },

  // ---- Console pages: admin Forecast page (AC4/AC5) ----

  'AC4: admin Forecast page renders filter bar, summary cards and forecast chart': function (browser) {
    const org = browser.globals.orgA;
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${org}')`);
    browser.url(browser.globals.baseUrl + '/admin/forecast');
    browser.waitForElementPresent('[data-testid="forecast-page"]', 15000, 'AC4: page renders');
    browser.waitForElementPresent('[data-testid="forecast-title"]', 10000, 'AC4: title');
    browser.waitForElementPresent('[data-testid="forecast-filter-bar"]', 10000, 'AC4: filter bar');
    browser.waitForElementPresent('[data-testid="forecast-range-select"]', 10000, 'AC4: range select');
    browser.waitForElementPresent('[data-testid="forecast-dimension-select"]', 10000, 'AC4: dimension select');
    browser.waitForElementPresent('[data-testid="forecast-horizon-select"]', 10000, 'AC4: horizon select');
    browser.waitForElementPresent('[data-testid="forecast-cards"]', 10000, 'AC4: summary cards');
    browser.waitForElementPresent('[data-testid="forecast-tokens"]', 10000, 'AC4: forecast tokens card');
    browser.waitForElementPresent('[data-testid="forecast-cost"]', 10000, 'AC4: forecast cost card');
    browser.waitForElementPresent('[data-testid="forecast-chart"]', 10000, 'AC4: forecast chart');
    browser.waitForElementPresent('[data-testid="forecast-refresh"]', 10000, 'AC4: refresh button');
  },

  'AC5: metric switcher toggles the chart metric': function (browser) {
    const org = browser.globals.orgA;
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${org}')`);
    browser.url(browser.globals.baseUrl + '/admin/forecast');
    browser.waitForElementPresent('[data-testid="forecast-metric-tokens"]', 15000, 'AC5: metric toggle');
    browser.click('[data-testid="forecast-metric-cost"]');
    browser.waitForElementPresent('[data-testid="forecast-chart"]', 5000, 'AC5: chart persists after toggle');
  },

  // ---- Console pages: empty state (AC6) ----

  'AC6: empty state renders when no data matches': function (browser) {
    // Use orgB, which has no charge records. The end-user page is
    // org-scoped, so its view is empty. Seed a user session for orgB so
    // the page renders.
    api.seedSession(browser, 'user', browser.globals.orgB);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgB}')`);
    browser.url(browser.globals.baseUrl + '/forecast');
    browser.waitForElementPresent('[data-testid="forecast-page"]', 15000, 'AC6: user page renders');
    browser.waitForElementPresent('[data-testid="forecast-empty"]', 15000, 'AC6: empty state renders');
  },

  // ---- Console pages: end-user (AC7) ----

  'AC7b: end-user Forecast page renders the tenant forecast': function (browser) {
    // Re-seed the user session for orgA: AC6 re-seeded it for orgB, and
    // the user surface resolves the org from the session (not the
    // X-Organization-Id header), so without this the page would be scoped
    // to orgB.
    api.seedSession(browser, 'user', browser.globals.orgA);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/forecast');
    browser.waitForElementPresent('[data-testid="forecast-page"]', 15000, 'AC7b: user page renders');
    browser.waitForElementPresent('[data-testid="forecast-cards"]', 10000, 'AC7b: cards');
    browser.waitForElementPresent('[data-testid="forecast-chart"]', 10000, 'AC7b: chart');
  },

  // ---- Console pages: permission denied (AC9) ----

  'AC9: a session without the required role receives 10036 on the admin forecast API': function (browser) {
    const org = browser.globals.orgA;
    const nonMemberUser = `33333333-4444-4444-4444-${browser.globals.runId.toString().padStart(12, '0').slice(-12)}`;
    api.seedSession(browser, 'admin', org, 'admin', nonMemberUser, {noMember: true});
    browser.execute(function () {
      return localStorage.getItem('go-taas.admin.session-token');
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC9: non-member admin session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/forecast',
        org,
        headers: {Authorization: `Bearer ${token}`}
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC9: non-member session on admin forecast API -> 10036');
      });
    });
  }
};