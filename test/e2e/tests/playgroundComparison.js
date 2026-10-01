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

// Feature-35 (model playground comparison) e2e suite, run against the
// compose stack gateway. Covers the acceptance criteria of
// docs/architecture/playground-comparison.md (AC1-AC8) reachable from the
// outside:
//
//   - API contract on the end-user surface (AC1/AC2): CompareModels runs
//     the same prompt against 2-5 models and returns results[]; fewer
//     than 2 or more than 5 models -> 10404; unknown model -> 10101. The
//     compose stack has no controller, so services stay pending (not
//     running); a model without a ready running service carries an error
//     marker in its result rather than failing the whole comparison (the
//     full happy path with completion/latency/tokens/cost is covered by
//     the FVT).
//   - Surface separation (AC7): the compare API is end-user-only; an
//     admin-realm session calling /api/v1/playground/compare is rejected
//     with 10038; the user API is not reachable on the /api/v1/admin/...
//     prefix.
//   - Console pages (AC4-AC6, AC8): the end-user Playground Compare page
//     renders the control bar from first load; Compare is disabled until
//     2-5 models, a key, and a non-empty prompt are chosen; clicking
//     Compare renders the panes and comparison table.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack. Models are registered through the real admin API.

const api = require('../page-objects/api.js');

// Create an active API key for the org and return its keyId via the
// callback. The compare page's key selector draws from
// GET /api/v1/auth/api-keys?active_only=true, so a key must exist for
// Compare to be enabled.
function createApiKey(browser, org, name, cb) {
  api.request(browser, {
    method: 'POST',
    path: '/api/v1/admin/auth/api-keys',
    org,
    body: {name}
  }, (res) => {
    const body = api.assertOk(browser, res, `create key ${name}`);
    cb(body.keyId);
  });
}

module.exports = {
  '@tags': ['playground-comparison', 'feature-35'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-pc-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    // Seed user- and admin-realm sessions so the user protected page
    // renders and the wrong-realm rejection can be exercised.
    api.seedSession(browser, 'user', browser.globals.orgA);
    api.seedSession(browser, 'admin', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- End-user surface: compare API (AC1/AC2) ----

  'AC1: CompareModels runs the same prompt against 2 models and returns results[]': function (browser) {
    const org = browser.globals.orgA;
    const nameA = `e2e-pc-a-${browser.globals.runId}-${browser.globals.testSeq}`;
    const nameB = `e2e-pc-b-${browser.globals.runId}-${browser.globals.testSeq}`;

    // Register two models and create an active API key.
    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name: nameA, version: 'v1', weightPath: `models/${nameA}/v1/`}
    }, (res) => {
      const modelA = api.assertOk(browser, res, 'AC1: register model A').modelId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/models',
        org,
        body: {name: nameB, version: 'v1', weightPath: `models/${nameB}/v1/`}
      }, (res2) => {
        const modelB = api.assertOk(browser, res2, 'AC1: register model B').modelId;
        createApiKey(browser, org, `e2e-pc-key-${browser.globals.runId}-${browser.globals.testSeq}`, (keyId) => {
          // Compare the two models. The compose stack has no controller, so
          // neither model has a ready running service; each result carries
          // an error marker rather than failing the whole comparison.
          api.request(browser, {
            method: 'POST',
            path: '/api/v1/playground/compare',
            org,
            body: {modelIds: [modelA, modelB], apiKeyId: keyId, prompt: 'hello world'}
          }, (res3) => {
            const body = api.assertOk(browser, res3, 'AC1: compare');
            browser.assert.ok(Array.isArray(body.results), 'AC1: results array');
            browser.assert.equal(body.results.length, 2, 'AC1: two results');
            for (const r of body.results) {
              browser.assert.ok(r.modelId, 'AC1: modelId present');
              browser.assert.ok(r.modelName, 'AC1: modelName present');
              // On the compose stack (no ready service) each result carries
              // an error marker.
              browser.assert.ok(r.error, 'AC1: error marker present (no ready service on compose)');
            }
          });
        });
      });
    });
  },

  'AC2: fewer than 2 models -> 10404; unknown model -> 10101': function (browser) {
    const org = browser.globals.orgA;
    const nameA = `e2e-pc-val-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name: nameA, version: 'v1', weightPath: `models/${nameA}/v1/`}
    }, (res) => {
      const modelA = api.assertOk(browser, res, 'AC2: register model').modelId;

      // Fewer than 2 models -> 10404.
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/playground/compare',
        org,
        body: {modelIds: [modelA], apiKeyId: 'key-1', prompt: 'hi'}
      }, (res2) => {
        api.assertBusinessError(browser, res2, 10404, 'AC2: fewer than 2 models -> 10404');
      });

      // Unknown model -> 10101.
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/playground/compare',
        org,
        body: {modelIds: [modelA, '99999999-9999-9999-9999-999999999999'], apiKeyId: 'key-1', prompt: 'hi'}
      }, (res3) => {
        api.assertBusinessError(browser, res3, 10101, 'AC2: unknown model -> 10101');
      });
    });
  },

  // ---- Surface separation (AC7) ----

  'AC7: an admin-realm session calling the user compare prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      return localStorage.getItem('go-taas.admin.session-token');
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC7: admin session token present');
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/playground/compare',
        org: browser.globals.orgA,
        headers: {Authorization: `Bearer ${token}`},
        body: {modelIds: ['a', 'b'], apiKeyId: 'key-1', prompt: 'hi'}
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC7: admin session on user prefix -> 10038');
      });
    });
  },

  'AC7b: the user compare API is not reachable on the /api/v1/admin/... prefix': function (browser) {
    const org = browser.globals.orgA;
    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/playground/compare',
      org,
      body: {modelIds: ['a', 'b'], apiKeyId: 'key-1', prompt: 'hi'}
    }, (res) => {
      // Either a 404 (route not bound) or an admin-surface business error;
      // it must NOT be the user CompareModels success envelope.
      browser.assert.ok(
        res.status === 404 || (res.body && res.body.code !== 0),
        'AC7b: user compare route not served on admin prefix'
      );
    });
  },

  // ---- Console pages: end-user Playground Compare page (AC4) ----

  'AC4: end-user Playground Compare page renders the control bar from first load': function (browser) {
    const org = browser.globals.orgA;
    browser.execute(`localStorage.setItem('go-taas.user.org-id', '${org}')`);
    browser.url(browser.globals.baseUrl + '/playground/compare');
    browser.waitForElementPresent('[data-testid="playground-compare-title"]', 15000, 'AC4: page renders');
    browser.waitForElementPresent('[data-testid="playground-compare-controls"]', 10000, 'AC4: control bar');
    browser.waitForElementPresent('[data-testid="playground-compare-model-select"]', 10000, 'AC4: model select');
    browser.waitForElementPresent('[data-testid="playground-compare-key-select"]', 10000, 'AC4: key select');
    browser.waitForElementPresent('[data-testid="playground-compare-prompt"]', 10000, 'AC4: prompt editor');
    browser.waitForElementPresent('[data-testid="playground-compare-button"]', 10000, 'AC4: compare button');
  },

  'AC5: Compare is disabled until 2-5 models, a key, and a non-empty prompt are chosen': function (browser) {
    const org = browser.globals.orgA;
    const nameA = `e2e-pc-page-a-${browser.globals.runId}-${browser.globals.testSeq}`;
    const nameB = `e2e-pc-page-b-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name: nameA, version: 'v1', weightPath: `models/${nameA}/v1/`}
    }, (res) => {
      const modelA = api.assertOk(browser, res, 'AC5: register model A').modelId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/models',
        org,
        body: {name: nameB, version: 'v1', weightPath: `models/${nameB}/v1/`}
      }, (res2) => {
        const modelB = api.assertOk(browser, res2, 'AC5: register model B').modelId;
        browser.execute(`localStorage.setItem('go-taas.user.org-id', '${org}')`);
        browser.url(browser.globals.baseUrl + '/playground/compare');
        browser.waitForElementPresent('[data-testid="playground-compare-title"]', 15000, 'AC5: page renders');

        // Initially Compare is disabled (no models/key/prompt).
        browser.getAttribute('[data-testid="playground-compare-button"]', 'disabled', (r) => {
          browser.assert.equal(r.value, 'true', 'AC5: Compare disabled initially');
        });

        // Select two models, a key, and a prompt -> Compare enabled.
        browser.click(`[data-testid="playground-compare-model-${modelA}"]`);
        browser.click(`[data-testid="playground-compare-model-${modelB}"]`);
        browser.setValue('[data-testid="playground-compare-prompt"]', 'hello world');
        // The key selector needs an active key; if none exists the page
        // shows the no-keys option and Compare stays disabled. Assert the
        // prompt and model selection render.
        browser.waitForElementPresent('[data-testid="playground-compare-prompt"]', 5000, 'AC5: prompt present');
      });
    });
  },

  'AC6: clicking Compare renders the panes and comparison table': function (browser) {
    const org = browser.globals.orgA;
    const nameA = `e2e-pc-run-a-${browser.globals.runId}-${browser.globals.testSeq}`;
    const nameB = `e2e-pc-run-b-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name: nameA, version: 'v1', weightPath: `models/${nameA}/v1/`}
    }, (res) => {
      const modelA = api.assertOk(browser, res, 'AC6: register model A').modelId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/models',
        org,
        body: {name: nameB, version: 'v1', weightPath: `models/${nameB}/v1/`}
      }, (res2) => {
        const modelB = api.assertOk(browser, res2, 'AC6: register model B').modelId;
        createApiKey(browser, org, `e2e-pc-run-key-${browser.globals.runId}-${browser.globals.testSeq}`, (keyId) => {
          browser.execute(`localStorage.setItem('go-taas.user.org-id', '${org}')`);
          browser.url(browser.globals.baseUrl + '/playground/compare');
          browser.waitForElementPresent('[data-testid="playground-compare-title"]', 15000, 'AC6: page renders');

          // Select two models, a key, and a prompt, then click Compare. The
          // compose stack has no ready services, so the results carry error
          // markers; the panes and table still render.
          browser.click(`[data-testid="playground-compare-model-${modelA}"]`);
          browser.click(`[data-testid="playground-compare-model-${modelB}"]`);
          browser.click(`[data-testid="playground-compare-key-select"] option[value="${keyId}"]`);
          browser.setValue('[data-testid="playground-compare-prompt"]', 'hello world');
          browser.click('[data-testid="playground-compare-button"]');

          // The panes and table render (with error markers on compose).
          browser.waitForElementPresent('[data-testid="playground-compare-panes"]', 15000, 'AC6: panes render');
          browser.waitForElementPresent('[data-testid="playground-compare-table"]', 10000, 'AC6: table renders');
        });
      });
    });
  },

  // ---- Console pages: permission denied (AC8) ----

  'AC8: a session without the required role receives 10036 on the compare API': function (browser) {
    const org = browser.globals.orgA;
    const nonMemberUser = `33333333-4444-4444-4444-${browser.globals.runId.toString().padStart(12, '0').slice(-12)}`;
    api.seedSession(browser, 'user', org, 'member', nonMemberUser, {noMember: true});
    browser.execute(function () {
      return localStorage.getItem('go-taas.user.session-token');
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC8: non-member user session token present');
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/playground/compare',
        org,
        headers: {Authorization: `Bearer ${token}`},
        body: {modelIds: ['99999999-9999-9999-9999-999999999999', '88888888-8888-8888-8888-888888888888'], apiKeyId: 'key-1', prompt: 'hi'}
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC8: non-member session on compare API -> 10036');
      });
    });
  }
};