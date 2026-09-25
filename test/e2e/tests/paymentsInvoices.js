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

// Feature-14 (payments, invoices & auto-recharge) e2e suite, run against
// the compose stack gateway. Covers the console acceptance criteria of
// docs/design/payments-invoices-auto-recharge.md (AC7-AC8). The
// payment-intent API flow (AC1-AC3) is covered by the unit and FVT
// suites.

const api = require('../page-objects/api.js');

module.exports = {
  '@tags': ['payments-invoices', 'feature-14'],

  beforeEach(browser) {
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-pay-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  'AC1: payment channels API is reachable': function (browser) {
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/billing/payment-channels?page.limit=5',
      org: browser.globals.orgA
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC1: payment channels reachable');
      browser.assert.ok(Array.isArray(body.channels), 'AC1: channels list returned');
    });
  },

  'AC7: payments page renders': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/billing/payments');
    browser.waitForElementPresent('[data-testid="payments-table"]', 15000, 'AC7: payments page renders');
  },

  'AC8: invoices page renders': function (browser) {
    browser.execute(`localStorage.setItem('go-taas.org-id', '${browser.globals.orgA}')`);
    browser.url(browser.globals.baseUrl + '/admin/billing/invoices');
    browser.waitForElementPresent('[data-testid="invoices-table"], [data-testid="invoices-empty"]', 15000, 'AC8: invoices page renders');
  }
};
