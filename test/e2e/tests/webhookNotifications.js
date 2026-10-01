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

// Feature-23 (webhook notifications & event subscriptions) e2e suite, run
// against the compose stack gateway. Covers the acceptance criteria of
// docs/design/webhook-notifications.md (AC1-AC16) reachable from the
// outside:
//
//   - API contract on the admin surface (AC1-AC7): create returns a
//     plaintext secret shown once, get never returns it; invalid config
//     10702 / bad event type 10705; list with search/filter/pagination and
//     delivery summary; update / set-enabled / delete; roll-secret; test
//     (ping) records a delivered delivery, disabled -> 10703; delivery log
//     with status filter and resend, unknown delivery -> 10704.
//   - API contract on the end-user surface (AC13): the four tenant events,
//     hard-scoped to the caller's org.
//   - Surface separation (AC14/AC15): admin pages call only
//     /api/v1/admin/webhooks/*, user pages call only /api/v1/webhooks/*;
//     a wrong-realm session is rejected with 10038; an admin event type on
//     the user surface (and vice versa) is rejected with 10705.
//   - Console pages (AC9-AC12, AC16): the admin list page renders the
//     event-catalog card and the webhooks table; the New Webhook dialog
//     validates each field and shows the secret-reveal dialog on success;
//     the detail page shows config, masked secret and delivery log; the
//     empty state renders; the user page renders only the four tenant
//     events; a session without the required role receives 10036.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack.

const api = require('../page-objects/api.js');

// Valid event catalogs (feature #23, §3.4).
const ADMIN_EVENTS = [
  'deployment.status_changed',
  'autoscaling.scaled',
  'autoscaling.scale_to_zero',
];
const USER_EVENTS = [
  'billing.invoice_created',
  'billing.invoice_paid',
  'billing.spend_limit_breached',
  'billing.balance_low',
];

// A local endpoint that is not listening, so a test/ping delivery fails
// (the deliverer records a failed delivery with a non-2xx / network error).
const DEAD_ENDPOINT = 'http://127.0.0.1:9/hook';

module.exports = {
  '@tags': ['webhook-notifications', 'feature-23'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-wh-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    // Seed admin- and user-realm sessions so both the admin and user
    // protected pages render (feature: unauthenticated pages redirect to
    // login). The suite navigates to both surfaces.
    api.seedSession(browser, 'admin', browser.globals.orgA);
    api.seedSession(browser, 'user', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- Admin surface: create / get (AC1) ----

  'AC1: create returns a plaintext secret once; get never returns it': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac1-${browser.globals.runId}-${browser.globals.testSeq}`;
    let webhookId = '';
    let createdSecret = '';

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: {
        name,
        url: DEAD_ENDPOINT,
        enabledEventTypes: ['deployment.status_changed'],
        maxAttempts: 3,
        backoffSeconds: 1,
      },
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC1: create webhook');
      webhookId = body.webhook.webhookId;
      createdSecret = body.plaintextSecret;
      browser.assert.ok(webhookId, 'AC1: webhookId returned');
      browser.assert.ok(createdSecret && createdSecret.startsWith('whsec_'), 'AC1: plaintext secret returned once');
      browser.assert.equal(body.webhook.surface, 'admin', 'AC1: surface is admin');
      browser.assert.equal(body.webhook.enabled, true, 'AC1: enabled default true');
      browser.assert.equal(body.webhook.maxAttempts, 3, 'AC1: maxAttempts echoed');
      browser.assert.equal(body.webhook.backoffSeconds, 1, 'AC1: backoff echoed');

      api.request(browser, {
        method: 'GET',
        path: `/api/v1/admin/webhooks/${webhookId}`,
        org,
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC1: get webhook');
        browser.assert.equal(body2.webhook.webhookId, webhookId, 'AC1: get returns same id');
        browser.assert.equal(body2.webhook.name, name, 'AC1: get returns name');
        browser.assert.equal(body2.webhook.plaintextSecret, undefined, 'AC1: get never returns the secret');
        browser.assert.equal(body2.plaintextSecret, undefined, 'AC1: no plaintext secret on get');
      });
    });
  },

  // ---- Admin surface: validation (AC2) ----

  'AC2: invalid config returns 10702; bad event type returns 10705': function (browser) {
    const org = browser.globals.orgA;
    const cases = [
      { label: 'empty name', body: { name: '', url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'] }, code: 10702 },
      { label: 'name too long', body: { name: 'x'.repeat(65), url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'] }, code: 10702 },
      { label: 'invalid url scheme', body: { name: 'a', url: 'ftp://example.com/hook', enabledEventTypes: ['deployment.status_changed'] }, code: 10702 },
      { label: 'no host', body: { name: 'a', url: 'https://', enabledEventTypes: ['deployment.status_changed'] }, code: 10702 },
      { label: 'empty events', body: { name: 'a', url: DEAD_ENDPOINT, enabledEventTypes: [] }, code: 10702 },
      { label: 'maxAttempts 11', body: { name: 'a', url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'], maxAttempts: 11 }, code: 10702 },
      { label: 'backoff 3601', body: { name: 'a', url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'], backoffSeconds: 3601 }, code: 10702 },
      { label: 'user event on admin surface', body: { name: 'a', url: DEAD_ENDPOINT, enabledEventTypes: ['billing.invoice_created'] }, code: 10705 },
    ];

    cases.forEach((c) => {
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/webhooks',
        org,
        body: c.body,
      }, (res) => {
        api.assertBusinessError(browser, res, c.code, `AC2: ${c.label}`);
      });
    });
  },

  // ---- Admin surface: list (AC3) ----

  'AC3: list returns webhooks with search, enabled filter, pagination and delivery summary': function (browser) {
    const org = browser.globals.orgA;
    const base = `e2e-wh-ac3-${browser.globals.runId}-${browser.globals.testSeq}`;
    const nameA = `${base}-alpha`;
    const nameB = `${base}-beta`;
    let idA = '';
    let idB = '';

    // Create two webhooks, then disable one.
    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: { name: nameA, url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'] },
    }, (res) => {
      idA = api.assertOk(browser, res, 'AC3: create A').webhook.webhookId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/webhooks',
        org,
        body: { name: nameB, url: DEAD_ENDPOINT, enabledEventTypes: ['autoscaling.scaled'] },
      }, (res2) => {
        idB = api.assertOk(browser, res2, 'AC3: create B').webhook.webhookId;
        api.request(browser, {
          method: 'POST',
          path: `/api/v1/admin/webhooks/${idB}:set-enabled`,
          org,
          body: { enabled: false },
        }, () => {
          // List all.
          api.request(browser, {
            method: 'GET',
            path: `/api/v1/admin/webhooks?page.limit=100`,
            org,
          }, (res3) => {
            const body = api.assertOk(browser, res3, 'AC3: list all');
            const ids = (body.webhooks || []).map((w) => w.webhookId);
            browser.assert.ok(ids.includes(idA), 'AC3: list contains A');
            browser.assert.ok(ids.includes(idB), 'AC3: list contains B');
            const rowA = body.webhooks.find((w) => w.webhookId === idA);
            browser.assert.equal(rowA.enabled, true, 'AC3: A enabled');
            browser.assert.ok('totalDeliveries' in rowA, 'AC3: delivery summary present');
            browser.assert.ok('deliveredCount' in rowA, 'AC3: deliveredCount present');
            browser.assert.ok('failedCount' in rowA, 'AC3: failedCount present');

            // Search by name.
            api.request(browser, {
              method: 'GET',
              path: `/api/v1/admin/webhooks?name=${encodeURIComponent(nameA)}&page.limit=100`,
              org,
            }, (res4) => {
              const body4 = api.assertOk(browser, res4, 'AC3: search by name');
              const names = (body4.webhooks || []).map((w) => w.name);
              browser.assert.ok(names.includes(nameA), 'AC3: search finds A');
              browser.assert.ok(!names.includes(nameB), 'AC3: search excludes B');

              // Filter by enabled=false.
              api.request(browser, {
                method: 'GET',
                path: `/api/v1/admin/webhooks?enabledFilter=true&enabled=false&page.limit=100`,
                org,
              }, (res5) => {
                const body5 = api.assertOk(browser, res5, 'AC3: filter disabled');
                const disabled = (body5.webhooks || []).filter((w) => w.webhookId === idB);
                browser.assert.ok(disabled.length === 1, 'AC3: disabled filter finds B');
                const enabledRows = (body5.webhooks || []).filter((w) => w.enabled);
                browser.assert.equal(enabledRows.length, 0, 'AC3: disabled filter excludes enabled');
              });
            });
          });
        });
      });
    });
  },

  // ---- Admin surface: update / set-enabled / delete (AC4) ----

  'AC4: update changes config; set-enabled pauses/resumes; delete removes': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac4-${browser.globals.runId}-${browser.globals.testSeq}`;
    let id = '';

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: { name, url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'], maxAttempts: 5, backoffSeconds: 60 },
    }, (res) => {
      id = api.assertOk(browser, res, 'AC4: create').webhook.webhookId;

      // Update name/url/events/retry.
      api.request(browser, {
        method: 'PATCH',
        path: `/api/v1/admin/webhooks/${id}`,
        org,
        body: {
          name: `${name}-updated`,
          url: 'http://127.0.0.1:8/hook2',
          enabledEventTypes: ['autoscaling.scaled', 'autoscaling.scale_to_zero'],
          maxAttempts: 7,
          backoffSeconds: 120,
        },
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC4: update');
        browser.assert.equal(body2.webhook.name, `${name}-updated`, 'AC4: name updated');
        browser.assert.equal(body2.webhook.url, 'http://127.0.0.1:8/hook2', 'AC4: url updated');
        browser.assert.equal(body2.webhook.maxAttempts, 7, 'AC4: maxAttempts updated');
        browser.assert.equal(body2.webhook.backoffSeconds, 120, 'AC4: backoff updated');
        browser.assert.ok(body2.webhook.enabledEventTypes.includes('autoscaling.scaled'), 'AC4: events updated');

        // Disable.
        api.request(browser, {
          method: 'POST',
          path: `/api/v1/admin/webhooks/${id}:set-enabled`,
          org,
          body: { enabled: false },
        }, (res3) => {
          const body3 = api.assertOk(browser, res3, 'AC4: disable');
          browser.assert.equal(body3.webhook.enabled, false, 'AC4: disabled');

          // Re-enable.
          api.request(browser, {
            method: 'POST',
            path: `/api/v1/admin/webhooks/${id}:set-enabled`,
            org,
            body: { enabled: true },
          }, (res4) => {
            const body4 = api.assertOk(browser, res4, 'AC4: re-enable');
            browser.assert.equal(body4.webhook.enabled, true, 'AC4: re-enabled');

            // Delete.
            api.request(browser, {
              method: 'DELETE',
              path: `/api/v1/admin/webhooks/${id}`,
              org,
            }, (res5) => {
              api.assertOk(browser, res5, 'AC4: delete');

              // Get after delete -> 10701.
              api.request(browser, {
                method: 'GET',
                path: `/api/v1/admin/webhooks/${id}`,
                org,
              }, (res6) => {
                api.assertBusinessError(browser, res6, 10701, 'AC4: get after delete -> 10701');
              });
            });
          });
        });
      });
    });
  },

  // ---- Admin surface: roll secret (AC5) ----

  'AC5: roll-secret regenerates the secret and returns the new plaintext once': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac5-${browser.globals.runId}-${browser.globals.testSeq}`;
    let id = '';
    let firstSecret = '';

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: { name, url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'] },
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC5: create');
      id = body.webhook.webhookId;
      firstSecret = body.plaintextSecret;

      api.request(browser, {
        method: 'POST',
        path: `/api/v1/admin/webhooks/${id}:roll-secret`,
        org,
        body: {},
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC5: roll secret');
        browser.assert.ok(body2.plaintextSecret && body2.plaintextSecret.startsWith('whsec_'), 'AC5: new plaintext secret returned once');
        browser.assert.notEqual(body2.plaintextSecret, firstSecret, 'AC5: new secret differs from old');

        // Get never returns the secret.
        api.request(browser, {
          method: 'GET',
          path: `/api/v1/admin/webhooks/${id}`,
          org,
        }, (res3) => {
          const body3 = api.assertOk(browser, res3, 'AC5: get after roll');
          browser.assert.equal(body3.webhook.plaintextSecret, undefined, 'AC5: get never returns secret');
        });
      });
    });
  },

  // ---- Admin surface: test / delivery log / resend (AC6, AC7) ----

  'AC6: test sends a ping and records a delivery; disabled webhook returns 10703': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac6-${browser.globals.runId}-${browser.globals.testSeq}`;
    let id = '';

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: { name, url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'], maxAttempts: 1, backoffSeconds: 1 },
    }, (res) => {
      id = api.assertOk(browser, res, 'AC6: create').webhook.webhookId;

      // Test against a dead endpoint -> the delivery is recorded as failed
      // (network error), but the RPC still succeeds (the delivery row is
      // written regardless of the endpoint outcome).
      api.request(browser, {
        method: 'POST',
        path: `/api/v1/admin/webhooks/${id}:test`,
        org,
        body: {},
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC6: test');
        browser.assert.ok(body2.delivery, 'AC6: delivery returned');
        browser.assert.equal(body2.delivery.eventType, 'webhook.ping', 'AC6: ping event type');
        browser.assert.ok(body2.delivery.deliveryId, 'AC6: delivery id returned');

        // Delivery log lists the ping delivery.
        api.request(browser, {
          method: 'GET',
          path: `/api/v1/admin/webhooks/${id}/deliveries?page.limit=100`,
          org,
        }, (res3) => {
          const body3 = api.assertOk(browser, res3, 'AC6: list deliveries');
          const pings = (body3.deliveries || []).filter((d) => d.eventType === 'webhook.ping');
          browser.assert.ok(pings.length >= 1, 'AC6: ping delivery in log');

          // Disable then test -> 10703.
          api.request(browser, {
            method: 'POST',
            path: `/api/v1/admin/webhooks/${id}:set-enabled`,
            org,
            body: { enabled: false },
          }, () => {
            api.request(browser, {
              method: 'POST',
              path: `/api/v1/admin/webhooks/${id}:test`,
              org,
              body: {},
            }, (res4) => {
              api.assertBusinessError(browser, res4, 10703, 'AC6: test disabled -> 10703');
            });
          });
        });
      });
    });
  },

  'AC7: delivery log filters by status; resend works; unknown delivery returns 10704': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac7-${browser.globals.runId}-${browser.globals.testSeq}`;
    let id = '';
    let deliveryId = '';

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: { name, url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'], maxAttempts: 1, backoffSeconds: 1 },
    }, (res) => {
      id = api.assertOk(browser, res, 'AC7: create').webhook.webhookId;

      // Test against a dead endpoint -> failed delivery.
      api.request(browser, {
        method: 'POST',
        path: `/api/v1/admin/webhooks/${id}:test`,
        org,
        body: {},
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC7: test');
        deliveryId = body2.delivery.deliveryId;
        browser.assert.ok(deliveryId, 'AC7: delivery id');

        // Filter by status=failed.
        api.request(browser, {
          method: 'GET',
          path: `/api/v1/admin/webhooks/${id}/deliveries?status=WEBHOOK_DELIVERY_STATUS_FAILED&page.limit=100`,
          org,
        }, (res3) => {
          const body3 = api.assertOk(browser, res3, 'AC7: filter failed');
          const failed = (body3.deliveries || []).filter((d) => d.deliveryId === deliveryId);
          browser.assert.ok(failed.length === 1, 'AC7: failed filter finds the delivery');

          // Resend the failed delivery.
          api.request(browser, {
            method: 'POST',
            path: `/api/v1/admin/webhooks/${id}/deliveries/${deliveryId}:resend`,
            org,
            body: {},
          }, (res4) => {
            const body4 = api.assertOk(browser, res4, 'AC7: resend');
            browser.assert.equal(body4.delivery.deliveryId, deliveryId, 'AC7: resend returns same delivery');

            // Resend an unknown delivery -> 10704.
            api.request(browser, {
              method: 'POST',
              path: `/api/v1/admin/webhooks/${id}/deliveries/00000000-0000-0000-0000-000000000000:resend`,
              org,
              body: {},
            }, (res5) => {
              api.assertBusinessError(browser, res5, 10704, 'AC7: resend unknown -> 10704');
            });
          });
        });
      });
    });
  },

  // ---- End-user surface (AC13) ----

  'AC13: user surface create/list/get/delete with the four tenant events': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac13-${browser.globals.runId}-${browser.globals.testSeq}`;
    let id = '';

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/webhooks',
      org,
      body: { name, url: DEAD_ENDPOINT, enabledEventTypes: ['billing.invoice_created', 'billing.balance_low'] },
    }, (res) => {
      const body = api.assertOk(browser, res, 'AC13: user create');
      id = body.webhook.webhookId;
      browser.assert.equal(body.webhook.surface, 'user', 'AC13: surface is user');
      browser.assert.ok(body.plaintextSecret && body.plaintextSecret.startsWith('whsec_'), 'AC13: secret returned once');

      api.request(browser, {
        method: 'GET',
        path: `/api/v1/webhooks/${id}`,
        org,
      }, (res2) => {
        const body2 = api.assertOk(browser, res2, 'AC13: user get');
        browser.assert.equal(body2.webhook.name, name, 'AC13: get returns name');
        browser.assert.equal(body2.webhook.plaintextSecret, undefined, 'AC13: get never returns secret');

        // List on the user surface.
        api.request(browser, {
          method: 'GET',
          path: '/api/v1/webhooks?page.limit=100',
          org,
        }, (res3) => {
          const body3 = api.assertOk(browser, res3, 'AC13: user list');
          const ids = (body3.webhooks || []).map((w) => w.webhookId);
          browser.assert.ok(ids.includes(id), 'AC13: user list contains the webhook');

          // Delete.
          api.request(browser, {
            method: 'DELETE',
            path: `/api/v1/webhooks/${id}`,
            org,
          }, (res4) => {
            api.assertOk(browser, res4, 'AC13: user delete');
          });
        });
      });
    });
  },

  'AC13: admin event type on the user surface returns 10705': function (browser) {
    const org = browser.globals.orgA;
    api.request(browser, {
      method: 'POST',
      path: '/api/v1/webhooks',
      org,
      body: { name: 'bad', url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'] },
    }, (res) => {
      api.assertBusinessError(browser, res, 10705, 'AC13: admin event on user surface -> 10705');
    });
  },

  // ---- Surface separation negative cases (AC14/AC15) ----

  'AC14: a user-realm session calling the admin prefix is rejected with 10038': function (browser) {
    // The user session token is in localStorage (go-taas.user.session-token).
    // The frontend sends it as Authorization: Bearer <token>. A user-realm
    // session on the admin prefix must be rejected by the realm guard.
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.user.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC14: user session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/webhooks?page.limit=5',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC14: user session on admin prefix -> 10038');
      });
    });
  },

  'AC15: an admin-realm session calling the user prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.admin.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC15: admin session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/webhooks?page.limit=5',
        org: browser.globals.orgA,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC15: admin session on user prefix -> 10038');
      });
    });
  },

  // ---- Console pages (AC9-AC12, AC16) ----

  'AC9: admin webhooks page renders the event-catalog card and the table': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac9-${browser.globals.runId}-${browser.globals.testSeq}`;
    let id = '';

    // Create a webhook so the table has a row.
    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: { name, url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'] },
    }, (res) => {
      id = api.assertOk(browser, res, 'AC9: create').webhook.webhookId;

      browser.execute(`localStorage.setItem('go-taas.org-id', '${org}')`);
      browser.url(browser.globals.baseUrl + '/admin/webhooks');
      browser.waitForElementPresent('[data-testid="webhook-catalog"]', 15000, 'AC9: event-catalog card renders');
      browser.waitForElementPresent('[data-testid="webhook-table"]', 15000, 'AC9: webhooks table renders');
      browser.waitForElementPresent(`[data-testid="webhook-row-${id}"]`, 15000, 'AC9: created webhook row renders');
      browser.waitForElementPresent('[data-testid="webhook-new"]', 10000, 'AC9: New webhook button');
      browser.waitForElementPresent('[data-testid="webhook-refresh"]', 10000, 'AC9: Refresh button');
      // The admin catalog lists exactly the three admin events.
      browser.assert.textContains('[data-testid="webhook-catalog"]', 'deployment.status_changed', 'AC9: admin catalog shows deployment event');
      browser.assert.textContains('[data-testid="webhook-catalog"]', 'autoscaling.scaled', 'AC9: admin catalog shows autoscaling.scaled');
      browser.assert.textContains('[data-testid="webhook-catalog"]', 'autoscaling.scale_to_zero', 'AC9: admin catalog shows scale_to_zero');
      browser.assert.not.textContains('[data-testid="webhook-catalog"]', 'billing.invoice_created', 'AC9: admin catalog never shows tenant events');
    });
  },

  'AC10: New Webhook dialog validates fields and shows the secret-reveal dialog on success': function (browser) {
    const org = browser.globals.orgA;
    browser.execute(`localStorage.setItem('go-taas.org-id', '${org}')`);
    browser.url(browser.globals.baseUrl + '/admin/webhooks');
    browser.waitForElementPresent('[data-testid="webhook-new"]', 15000, 'AC10: page renders');
    browser.click('[data-testid="webhook-new"]');
    browser.waitForElementPresent('[data-testid="webhook-create-dialog"]', 10000, 'AC10: create dialog opens');

    // Submit with empty fields -> validation error.
    browser.click('[data-testid="webhook-dialog-submit"]');
    browser.waitForElementPresent('[data-testid="webhook-dialog-error"]', 10000, 'AC10: validation error shown');

    // Fill valid fields.
    browser.setValue('[data-testid="webhook-dialog-name"]', `e2e-wh-ac10-${browser.globals.runId}-${browser.globals.testSeq}`);
    browser.setValue('[data-testid="webhook-dialog-url"]', DEAD_ENDPOINT);
    // Select the first admin event checkbox.
    browser.click('[data-testid="webhook-dialog-events"] input[type="checkbox"]');
    browser.setValue('[data-testid="webhook-dialog-max-attempts"]', '3');
    browser.setValue('[data-testid="webhook-dialog-backoff"]', '5');
    browser.click('[data-testid="webhook-dialog-submit"]');

    // Secret-reveal dialog appears with the plaintext secret.
    browser.waitForElementPresent('[data-testid="webhook-secret-reveal"]', 15000, 'AC10: secret-reveal dialog');
    browser.waitForElementPresent('[data-testid="webhook-secret-value"]', 10000, 'AC10: secret value shown');
    browser.assert.textContains('[data-testid="webhook-secret-value"]', 'whsec_', 'AC10: secret starts with whsec_');

    // Done navigates to the detail page.
    browser.click('[data-testid="webhook-secret-done"]');
    browser.waitForElementPresent('[data-testid="webhook-detail-config"]', 15000, 'AC10: navigates to detail page');
  },

  'AC11: detail page shows config, masked secret and delivery log; test records a delivery': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-wh-ac11-${browser.globals.runId}-${browser.globals.testSeq}`;
    let id = '';

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/webhooks',
      org,
      body: { name, url: DEAD_ENDPOINT, enabledEventTypes: ['deployment.status_changed'], maxAttempts: 1, backoffSeconds: 1 },
    }, (res) => {
      id = api.assertOk(browser, res, 'AC11: create').webhook.webhookId;

      browser.execute(`localStorage.setItem('go-taas.org-id', '${org}')`);
      browser.url(browser.globals.baseUrl + `/admin/webhooks/${id}`);
      browser.waitForElementPresent('[data-testid="webhook-detail-config"]', 15000, 'AC11: config card renders');
      browser.waitForElementPresent('[data-testid="webhook-secret-masked"]', 10000, 'AC11: masked secret card renders');
      browser.assert.textContains('[data-testid="webhook-secret-masked"]', 'whsec_', 'AC11: secret is masked');
      browser.waitForElementPresent('[data-testid="webhook-delivery-empty"]', 10000, 'AC11: delivery log empty initially');

      // Click the Test button (no testid on the button; select by text via
      // XPath). The delivery log should then show a row (the ping delivery).
      browser.useXpath().click('//button[normalize-space()="Test"]').useCss();
      browser.waitForElementPresent('[data-testid="webhook-delivery-table"]', 15000, 'AC11: delivery log shows a row after test');
      browser.assert.textContains('[data-testid="webhook-delivery-table"]', 'webhook.ping', 'AC11: ping delivery in log');
    });
  },

  'AC12: empty state renders when the list is empty': function (browser) {
    const org = browser.globals.orgA;
    // Use a fresh org with no webhooks.
    const emptyOrg = `org-e2e-wh-empty-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, emptyOrg);
    api.seedSession(browser, 'admin', emptyOrg);
    browser.execute(`localStorage.setItem('go-taas.org-id', '${emptyOrg}')`);
    browser.url(browser.globals.baseUrl + '/admin/webhooks');
    browser.waitForElementPresent('[data-testid="webhook-empty"]', 15000, 'AC12: empty state renders');
    browser.assert.textContains('[data-testid="webhook-empty"]', 'No webhooks yet', 'AC12: empty copy');
  },

  'AC13: user webhooks page renders only the four tenant events': function (browser) {
    const org = browser.globals.orgA;
    browser.execute(`localStorage.setItem('go-taas.org-id', '${org}')`);
    browser.url(browser.globals.baseUrl + '/webhooks');
    browser.waitForElementPresent('[data-testid="webhook-catalog"]', 15000, 'AC13: user page renders');
    browser.assert.textContains('[data-testid="webhook-catalog"]', 'billing.invoice_created', 'AC13: user catalog shows invoice_created');
    browser.assert.textContains('[data-testid="webhook-catalog"]', 'billing.invoice_paid', 'AC13: user catalog shows invoice_paid');
    browser.assert.textContains('[data-testid="webhook-catalog"]', 'billing.spend_limit_breached', 'AC13: user catalog shows spend_limit_breached');
    browser.assert.textContains('[data-testid="webhook-catalog"]', 'billing.balance_low', 'AC13: user catalog shows balance_low');
    browser.assert.not.textContains('[data-testid="webhook-catalog"]', 'deployment.status_changed', 'AC13: user catalog never shows admin events');
  },

  'AC14: admin webhook routes are not served on the user console': function (browser) {
    const org = browser.globals.orgA;
    browser.execute(`localStorage.setItem('go-taas.org-id', '${org}')`);
    // The user console must not serve the admin webhook route; navigating
    // to /admin/webhooks from the user surface redirects to the user home.
    browser.url(browser.globals.baseUrl + '/admin/webhooks');
    browser.pause(2000);
    browser.assert.urlContains('/webhooks', 'AC14: /admin/webhooks redirects away from the admin route on the user console');
  },

  'AC16: a session without the required role receives 10036 on the admin webhook API': function (browser) {
    // The admin webhook RPCs are gated by tenancy.RoleGuard at the minimum
    // role "member" (services/webhook/service.go roleMember). The RoleGuard
    // resolves the caller's role from the org_members table, so a session
    // whose user is NOT a member of the org must be denied with 10036.
    const org = browser.globals.orgA;
    // A valid UUID user that is NOT added to org_members (noMember), so the
    // RoleGuard resolves no role and returns 10036.
    const nonMemberUser = `33333333-4444-4444-4444-${browser.globals.runId.toString().padStart(12, '0').slice(-12)}`;
    api.seedSession(browser, 'admin', org, 'admin', nonMemberUser, { noMember: true });
    browser.execute(function () {
      const token = localStorage.getItem('go-taas.admin.session-token');
      return token;
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC16: non-member admin session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/webhooks?page.limit=5',
        org,
        headers: { Authorization: `Bearer ${token}` },
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC16: non-member session on admin webhook API -> 10036');
      });
    });
  },
};