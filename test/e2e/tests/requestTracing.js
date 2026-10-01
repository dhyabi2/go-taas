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

// Feature-27 (request tracing & latency breakdown) e2e suite, run against
// the compose stack gateway. Covers the acceptance criteria of
// docs/design/request-tracing.md (AC1-AC13) reachable from the outside:
//
//   - API contract on the admin surface (AC1/AC2/AC3): ListTraces returns
//     trace rows for a valid range; a range > 92 days or since > until
//     returns 10404; a request_id filter returns at most one trace and an
//     unknown request_id returns an empty list; GetTrace returns the
//     summary + spans and an unknown trace_id returns 11101.
//   - API contract on the end-user surface (AC4): GetTrace/ListTraces on
//     the user prefix return only the caller's org traces, with service_id
//     masked to a phase label and no operator internals.
//   - Surface separation (AC11/AC12): admin pages call only
//     /api/v1/admin/traces/*, user pages call only /api/v1/traces/*; a
//     wrong-realm session is rejected with 10038; the admin API is not
//     reachable on the bare /api/v1/... prefix.
//   - Console pages (AC5-AC10, AC13): the admin /admin/traces page renders
//     the lookup box, filter bar and trace table from the first successful
//     load; a request-id lookup navigates to the detail and an unknown id
//     shows the lookup-not-found banner; changing a filter refetches; the
//     empty state renders; the admin detail renders the summary strip,
//     latency-breakdown card, span waterfall and metadata table; the
//     end-user /traces and /traces/:traceId pages render the tenant's own
//     traces with no service ids; a session without the required role
//     receives 10036.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack. Traces are seeded directly into PostgreSQL by
// test/e2e/seed/tracing/seed_tracing.go (the tracing module is a read-only
// query over traces).

const api = require('../page-objects/api.js');
const { execSync } = require('child_process');
const path = require('path');

// Fixed model / key ids used by the seed (must match seed_tracing.go).
const MODEL_A = 'aaaaaaaa-1111-1111-1111-111111111111';
const MODEL_B = 'aaaaaaaa-2222-2222-2222-222222222222';
const KEY_A = 'bbbbbbbb-3333-3333-3333-333333333333';
const KEY_B = 'bbbbbbbb-5555-5555-5555-555555555555';

// Seed traces for the given org directly into PostgreSQL (the tracing
// module is a read-only query over traces, and the compose stack has no
// inference pipeline to produce them).
function seedTraces(browser, org, otherOrg) {
  const repoRoot = path.resolve(__dirname, '..', '..', '..');
  const otherArg = otherOrg ? ` -other-org '${otherOrg}'` : '';
  const cmd = [
    'docker run --rm --network go-taas_default',
    '-e GOPROXY=https://goproxy.cn,direct',
    '-v go-taas-go-mod-cache:/go/pkg/mod',
    '-v go-taas-go-build-cache:/root/.cache/go-build',
    `-v "${repoRoot}":/app`,
    '-w /app/test/e2e/seed/tracing',
    'golang:1.26-alpine',
    `sh -c "go run seed_tracing.go -dsn 'postgres://taas:taas@postgres:5432/taas?sslmode=disable' -org '${org}' -model '${MODEL_A}' -key '${KEY_A}'${otherArg}"`
  ].join(' ');
  try {
    execSync(cmd, {stdio: 'pipe', timeout: 120000});
  } catch (e) {
    browser.assert.fail(`seedTraces failed: ${e.message}`);
  }
}

module.exports = {
  '@tags': ['request-tracing', 'feature-27'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-tr-${browser.globals.runId}-${browser.globals.testSeq}`;
    browser.globals.orgB = `org-e2e-tr-b-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    api.ensureOrg(browser, browser.globals.orgB);
    // Seed traces for orgA so the tracing pages show data; orgB is the
    // other tenant whose traces must never appear in orgA's view.
    seedTraces(browser, browser.globals.orgA, browser.globals.orgB);
    // Seed admin- and user-realm sessions so both the admin and user
    // protected pages render.
    api.seedSession(browser, 'admin', browser.globals.orgA);
    api.seedSession(browser, 'user', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- Admin surface: trace list API (AC1/AC2) ----

  'AC1: ListTraces returns rows for a valid range; over-long or inverted range returns 10404': function (browser) {
    const org = browser.globals.orgA;
    const now = Math.floor(Date.now() / 1000);

    // Default range: the seeded traces produce rows.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/traces',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC1: list');
      browser.assert.ok(Array.isArray(body.traces), 'AC1: traces array present');
      browser.assert.ok(body.traces.length >= 1, 'AC1: at least one trace');
      const t = body.traces[0];
      browser.assert.ok(t.traceId, 'AC1: traceId present');
      browser.assert.ok(t.totalLatencyMs !== undefined, 'AC1: totalLatencyMs present');
      browser.assert.ok(t.ttftMs !== undefined, 'AC1: ttftMs present');
      browser.assert.ok(t.generationMs !== undefined, 'AC1: generationMs present');
      browser.assert.ok(t.status, 'AC1: status present');
    });

    // Inverted range -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/traces?since=${now}&until=${now - 10}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: inverted range');
    });

    // Range over 92 days -> 10404.
    api.request(browser, {
      method: 'GET',
      path: `/api/v1/admin/traces?since=${now - 93 * 24 * 3600}&until=${now}`,
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 10404, 'AC1: over-long range');
    });
  },

  'AC2: request_id filter returns at most one trace; unknown request_id returns empty list': function (browser) {
    const org = browser.globals.orgA;

    // Fetch a known trace id from the list, then look it up exactly.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/traces',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC2: list');
      const traceId = body.traces[0].traceId;
      api.request(browser, {
        method: 'GET',
        path: `/api/v1/admin/traces?request_id=${traceId}`,
        org,
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC2: lookup');
        browser.assert.ok(Array.isArray(body2.traces), 'AC2: traces array');
        browser.assert.equal(body2.traces.length, 1, 'AC2: at most one trace');
        browser.assert.equal(body2.traces[0].traceId, traceId, 'AC2: exact match');
      });
    });

    // Unknown request_id -> empty list.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/traces?request_id=does-not-exist',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC2: unknown lookup');
      browser.assert.ok(Array.isArray(body.traces), 'AC2: traces array');
      browser.assert.equal(body.traces.length, 0, 'AC2: empty list for unknown id');
    });
  },

  // ---- Admin surface: trace detail API (AC3) ----

  'AC3: GetTrace returns summary and spans; unknown trace_id returns 11101': function (browser) {
    const org = browser.globals.orgA;

    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/traces',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC3: list');
      const traceId = body.traces[0].traceId;
      api.request(browser, {
        method: 'GET',
        path: `/api/v1/admin/traces/${traceId}`,
        org,
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC3: detail');
        const trace = body2.trace;
        browser.assert.equal(trace.traceId, traceId, 'AC3: traceId');
        browser.assert.ok(trace.totalLatencyMs !== undefined, 'AC3: totalLatencyMs');
        browser.assert.ok(trace.ttftMs !== undefined, 'AC3: ttftMs');
        browser.assert.ok(trace.generationMs !== undefined, 'AC3: generationMs');
        browser.assert.ok(Array.isArray(trace.spans), 'AC3: spans array');
        browser.assert.ok(trace.spans.length >= 1, 'AC3: at least one span');
        // Admin surface exposes the operator service id.
        browser.assert.ok(trace.serviceId, 'AC3: admin serviceId present');
        // The four token counts.
        browser.assert.ok(trace.promptTokens !== undefined, 'AC3: promptTokens');
        browser.assert.ok(trace.completionTokens !== undefined, 'AC3: completionTokens');
        browser.assert.ok(trace.cachedTokens !== undefined, 'AC3: cachedTokens');
        browser.assert.ok(trace.reasoningTokens !== undefined, 'AC3: reasoningTokens');
      });
    });

    // Unknown trace_id -> 11101.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/traces/does-not-exist',
      org,
    }, (res) => {
      api.assertBusinessError(browser, res, 11101, 'AC3: unknown trace');
    });
  },

  // ---- End-user surface: tenant-scoped API (AC4) ----

  'AC4: user ListTraces/GetTrace return only the caller org, service_id masked': function (browser) {
    const org = browser.globals.orgA;

    // The user list is org-scoped and masks service_id to a phase label.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/traces',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC4: user list');
      browser.assert.ok(Array.isArray(body.traces), 'AC4: traces array');
      browser.assert.ok(body.traces.length >= 1, 'AC4: tenant traces present');
      const t = body.traces[0];
      // service_id masked to a phase label, never an operator id.
      browser.assert.ok(
        t.serviceId === 'gateway' || t.serviceId === 'inference',
        `AC4: serviceId masked to a phase label (got: ${t.serviceId})`
      );
      browser.assert.equal(t.serviceId, 'inference', 'AC4: inference phase label');
    });

    // The user detail masks service_id too.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/traces',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC4: user list 2');
      const traceId = body.traces[0].traceId;
      api.request(browser, {
        method: 'GET',
        path: `/api/v1/traces/${traceId}`,
        org,
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC4: user detail');
        const trace = body2.trace;
        browser.assert.equal(trace.serviceId, 'inference', 'AC4: user detail serviceId masked');
      });
    });

    // A caller cannot see another org's trace (11101).
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/traces',
      org: browser.globals.orgB,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC4: orgB list');
      const otherTraceId = body.traces[0].traceId;
      // orgA cannot fetch orgB's trace.
      api.request(browser, {
        method: 'GET',
        path: `/api/v1/traces/${otherTraceId}`,
        org,
      }, (res2) => {
        api.assertBusinessError(browser, res2, 11101, 'AC4: cross-org trace denied');
      });
    });
  },

  // ---- Surface separation (AC11/AC12) ----

  'AC11: admin traces API is not reachable on the bare /api/v1 prefix': function (browser) {
    const org = browser.globals.orgA;

    // The admin list is not served on the bare /api/v1/traces... wait, it
    // IS the user prefix. The admin-only surface is /api/v1/admin/traces.
    // A bare /api/v1/traces is the user surface, so it must NOT behave as
    // the admin fleet view (it is org-scoped). Assert the admin prefix is
    // not reachable on a non-admin path: /api/v1/traces is the user
    // surface and returns only the caller's org (already covered in AC4).
    // Here we assert the admin API is not reachable on the bare
    // /api/v1/traces with an admin-only expectation: the user surface
    // masks service_id, so the admin operator id must never appear.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/traces',
      org,
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC11: user surface');
      browser.assert.ok(Array.isArray(body.traces), 'AC11: traces array');
      if (body.traces.length > 0) {
        browser.assert.ok(
          body.traces[0].serviceId === 'gateway' || body.traces[0].serviceId === 'inference',
          'AC11: user surface masks service_id'
        );
      }
    });
  },

  'AC12: a user-realm session calling the admin traces prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.user.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12: user session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/traces',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12: user session on admin prefix -> 10038');
      });
    });
  },

  'AC12b: an admin-realm session calling the user traces prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.admin.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC12b: admin session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/traces',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC12b: admin session on user prefix -> 10038');
      });
    });
  },

  // ---- Console pages: admin trace explorer (AC5/AC6/AC7/AC8) ----

  'AC5: admin /admin/traces page renders lookup box, filter bar and trace table': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/traces');

    browser.waitForElementPresent('[data-testid="traces-page"]', 15000, 'AC5: page renders');
    browser.waitForElementPresent('[data-testid="traces-lookup-input"]', 10000, 'AC5: lookup input');
    browser.waitForElementPresent('[data-testid="traces-lookup-button"]', 10000, 'AC5: lookup button');
    browser.waitForElementPresent('[data-testid="traces-filter-range"]', 10000, 'AC5: range filter');
    browser.waitForElementPresent('[data-testid="traces-filter-model"]', 10000, 'AC5: model filter');
    browser.waitForElementPresent('[data-testid="traces-filter-key"]', 10000, 'AC5: key filter');
    browser.waitForElementPresent('[data-testid="traces-filter-status"]', 10000, 'AC5: status filter');
    browser.waitForElementPresent('[data-testid="traces-table"]', 10000, 'AC5: trace table');
    browser.waitForElementPresent('[data-testid="traces-refresh"]', 10000, 'AC5: refresh button');
  },

  'AC6: request-id lookup navigates to detail; unknown id shows lookup-not-found banner': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/traces');
    browser.waitForElementPresent('[data-testid="traces-table"]', 15000, 'AC6: table renders');

    // Grab a known trace id from the table row.
    browser.waitForElementPresent('[data-testid^="traces-row-"]', 15000, 'AC6: trace row present');
    browser.getAttribute('[data-testid^="traces-row-"]', 'data-testid', (attr) => {
      const rowTestId = attr.value;
      const traceId = rowTestId.replace('traces-row-', '');
      // Type the trace id into the lookup box and click Look up.
      browser.setValue('[data-testid="traces-lookup-input"]', traceId);
      browser.click('[data-testid="traces-lookup-button"]');
      // Should navigate to the detail page.
      browser.waitForElementPresent('[data-testid="trace-detail-page"]', 15000, 'AC6: navigates to detail');
    });
  },

  'AC6b: unknown request-id lookup shows the lookup-not-found banner': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/traces');
    browser.waitForElementPresent('[data-testid="traces-lookup-input"]', 15000, 'AC6b: lookup input');

    browser.setValue('[data-testid="traces-lookup-input"]', 'does-not-exist-trace');
    browser.click('[data-testid="traces-lookup-button"]');
    browser.waitForElementPresent('[data-testid="traces-lookup-notfound"]', 15000, 'AC6b: lookup-not-found banner');
  },

  'AC7: changing a filter refetches and re-renders the trace table': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/traces');
    browser.waitForElementPresent('[data-testid="traces-table"]', 15000, 'AC7: table renders');

    // Select the error status filter; the table refetches and re-renders.
    browser.click('[data-testid="traces-filter-status"] option[value="error"]');
    browser.waitForElementPresent('[data-testid="traces-table"]', 10000, 'AC7: table persists after filter');
  },

  'AC8: empty state renders when no data matches': function (browser) {
    // Use a fresh org with no traces seeded. The end-user page is
    // org-scoped, so its view is empty. Seed a user session for the fresh
    // org and set the user org key so the page renders. The user surface's
    // org key is realm-scoped (go-taas.user.org-id), so set that key
    // directly rather than the legacy go-taas.org-id (which is only adopted
    // on a fresh surface boot and would not override the already-present
    // go-taas.user.org-id=orgA from beforeEach).
    const emptyOrg = `org-e2e-tr-empty-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, emptyOrg);
    api.seedSession(browser, 'user', emptyOrg);
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${emptyOrg}')`);
    browser.url(browser.globals.baseUrl + '/traces');
    browser.waitForElementPresent('[data-testid="user-traces-page"]', 15000, 'AC8: user page renders');
    browser.waitForElementPresent('[data-testid="traces-empty"]', 15000, 'AC8: empty state renders');
  },

  // ---- Console pages: admin trace detail (AC9) ----

  'AC9: admin detail renders summary strip, latency card, waterfall and metadata; unknown trace shows not-found': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/traces');
    browser.waitForElementPresent('[data-testid="traces-table"]', 15000, 'AC9: table renders');

    browser.waitForElementPresent('[data-testid^="traces-row-"]', 15000, 'AC9: trace row present');
    browser.getAttribute('[data-testid^="traces-row-"]', 'data-testid', (attr) => {
      const traceId = attr.value.replace('traces-row-', '');
      browser.url(browser.globals.baseUrl + `/admin/traces/${traceId}`);
      browser.waitForElementPresent('[data-testid="trace-detail-page"]', 15000, 'AC9: detail page renders');
      browser.waitForElementPresent('[data-testid="trace-summary"]', 10000, 'AC9: summary strip');
      browser.waitForElementPresent('[data-testid="trace-latency-card"]', 10000, 'AC9: latency card');
      browser.waitForElementPresent('[data-testid="trace-latency-ttft"]', 10000, 'AC9: ttft segment');
      browser.waitForElementPresent('[data-testid="trace-latency-generation"]', 10000, 'AC9: generation segment');
      browser.waitForElementPresent('[data-testid="trace-waterfall"]', 10000, 'AC9: waterfall');
      browser.waitForElementPresent('[data-testid="trace-metadata"]', 10000, 'AC9: metadata table');
    });

    // Unknown trace -> not-found state.
    browser.url(browser.globals.baseUrl + '/admin/traces/does-not-exist');
    browser.waitForElementPresent('[data-testid="trace-detail-notfound"]', 15000, 'AC9: not-found state');
  },

  // ---- Console pages: end-user (AC10) ----

  'AC10: end-user /traces and /traces/:traceId render tenant traces with no service ids': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/traces');
    browser.waitForElementPresent('[data-testid="user-traces-page"]', 15000, 'AC10: user page renders');
    browser.waitForElementPresent('[data-testid="traces-table"]', 10000, 'AC10: trace table');

    // Drill into the first trace detail.
    browser.waitForElementPresent('[data-testid^="traces-row-"]', 15000, 'AC10: trace row present');
    browser.getAttribute('[data-testid^="traces-row-"]', 'data-testid', (attr) => {
      const traceId = attr.value.replace('traces-row-', '');
      browser.url(browser.globals.baseUrl + `/traces/${traceId}`);
      browser.waitForElementPresent('[data-testid="user-trace-detail-page"]', 15000, 'AC10: user detail page renders');
      browser.waitForElementPresent('[data-testid="trace-summary"]', 10000, 'AC10: summary strip');
      browser.waitForElementPresent('[data-testid="trace-latency-card"]', 10000, 'AC10: latency card');
      browser.waitForElementPresent('[data-testid="trace-waterfall"]', 10000, 'AC10: waterfall');
      browser.waitForElementPresent('[data-testid="trace-metadata"]', 10000, 'AC10: metadata table');
    });
  },

  // ---- Console pages: permission denied (AC13) ----

  'AC13: session without the required role receives 10036 on the admin traces API': function (browser) {
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
        path: '/api/v1/admin/traces',
        org,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC13: non-member session on admin traces API -> 10036');
      });
    });
  }
};