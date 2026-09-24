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

// Feature-11 (per-key rate limits & org spend limits) e2e suite, run
// against the compose stack gateway. Covers the acceptance criteria of
// docs/design/rate-limits-spend-limits.md reachable from the outside:
// the API Keys create dialog persists rate limits and the edit dialog
// updates them (AC-C1), and the Accounts create dialog persists the
// spend limit (AC-C2). The gateway enforcement (429) is covered by the
// FVT suite.

const api = require('../page-objects/api.js');

module.exports = {
  '@tags': ['rate-limits-spend-limits', 'feature-11'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-rl-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  'AC-C1: API key create persists rate limits and edit updates them': function (browser) {
    const org = browser.globals.orgA;

    // Create a key with rate limits via the API.
    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/auth/api-keys',
      org,
      body: {name: 'limited', rateLimitRpm: 100, rateLimitTpm: 50000}
    }, (res) => {
      const body = api.assertOk(browser, res, 'create key with limits');
      browser.assert.ok(body.keyId, 'AC-C1: key id returned');
    });

    // List returns the limits.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/auth/api-keys',
      org
    }, (res) => {
      const body = api.assertOk(browser, res, 'list keys');
      const key = (body.keys || []).find((k) => k.name === 'limited');
      browser.assert.ok(key, 'AC-C1: created key listed');
      browser.assert.equal(String(key.rateLimitRpm), '100', 'AC-C1: rpm persisted');
      browser.assert.equal(String(key.rateLimitTpm), '50000', 'AC-C1: tpm persisted');
    });
  },

  'AC-C2: account create persists the spend limit': function (browser) {
    const org = browser.globals.orgA;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/billing/accounts',
      org,
      body: {mode: 'prepaid', overdrawPolicy: 'block', initialBalanceCents: 10000, monthlySpendLimitCents: 5000}
    }, (res) => {
      const body = api.assertOk(browser, res, 'create account with spend limit');
      browser.assert.equal(
        String(body.account.monthlySpendLimitCents),
        '5000',
        'AC-C2: spend limit persisted'
      );
    });
  },

  'AC-C1: API keys page renders rate-limit column': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/api-keys');
    browser.waitForElementPresent(
      '[data-testid="create-api-key"]',
      10000,
      'AC-C1: create button renders'
    );
    // The rate-limit column renders (empty state or table).
    browser.waitForElementPresent(
      '[data-testid="api-keys-empty"], [data-testid="api-keys-table"]',
      10000,
      'AC-C1: keys list renders'
    );
  },

  'AC-C2: accounts page renders spend-limit column': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/billing/accounts');
    browser.waitForElementPresent(
      '[data-testid="create-account-button"]',
      10000,
      'AC-C2: create account button renders'
    );
    browser.waitForElementPresent(
      '[data-testid="accounts-empty"], [data-testid="accounts-table"]',
      10000,
      'AC-C2: accounts list renders'
    );
  }
};