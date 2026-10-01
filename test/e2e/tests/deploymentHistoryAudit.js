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

// Feature-34 (deployment history & audit) e2e suite, run against the
// compose stack gateway. Covers the acceptance criteria of
// docs/architecture/deployment-history-audit.md (AC1-AC9) reachable from
// the outside:
//
//   - API contract on the admin surface (AC1/AC2/AC3): ListDeploymentEvents
//     returns the trail newest first with service_id/service_name/
//     event_type/actor/before-after diff/created_at; unknown service_id ->
//     10301. The before/after diff covers replicas/model_version/image_id/
//     accelerator/accelerator_type/autoscaling, omitting unchanged fields.
//     RollbackDeployment reverts to the target event's before, keeps
//     service_id, transitions to deploying, records a new rollback event;
//     unknown service -> 10301, unknown event -> 10304.
//   - Surface separation (AC8): the deployment APIs are admin-only; a
//     user-realm session calling /api/v1/admin/deployments is rejected
//     with 10038; the admin API is not reachable on the bare /api/v1/...
//     prefix.
//   - Console pages (AC4, AC9): the admin Deployment History page renders
//     the filter bar and the event trail table from first load; a session
//     without the required role receives 10036.
//
// The compose stack has no controller and no interactive IdP login, so the
// suites authenticate with a seeded server-side session (see
// page-objects/api.js seedSession) and drive the real UI served by the
// compose stack. Inference services are created and scaled through the
// real admin API, which records the deployment event trail.

const api = require('../page-objects/api.js');

module.exports = {
  '@tags': ['deployment-history-audit', 'feature-34'],

  beforeEach(browser) {
    // Land on the gateway origin so in-page fetch() calls are same-origin.
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-dh-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    // Seed admin- and user-realm sessions so both the admin protected page
    // renders and the wrong-realm rejection can be exercised.
    api.seedSession(browser, 'admin', browser.globals.orgA);
    api.seedSession(browser, 'user', browser.globals.orgA);
  },

  afterEach(browser) {
    browser.end();
  },

  // ---- Admin surface: deployment event trail API (AC1/AC2) ----

  'AC1: ListDeploymentEvents returns the trail newest first; unknown service -> 10301': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-dh-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name, version: 'v1', weightPath: `models/${name}/v1/`}
    }, (res) => {
      const modelId = api.assertOk(browser, res, 'AC1: register model').modelId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/inference-services',
        org,
        body: {
          name: `svc-${browser.globals.runId}-${browser.globals.testSeq}`,
          modelId,
          modelVersion: 'v1',
          imageId: 'img-vllm-nvidia-v063',
          replicas: '2',
          accelerator: 'nvidia',
          acceleratorType: 'gpu'
        }
      }, (res2) => {
        const serviceId = api.assertOk(browser, res2, 'AC1: create service').serviceId;
        browser.assert.ok(Boolean(serviceId), 'AC1: serviceId returned');

        // The create records a create event.
        api.request(browser, {
          method: 'GET',
          path: `/api/v1/admin/deployments/events?service_id=${serviceId}`,
          org
        }, (res3) => {
          const body = api.assertOk(browser, res3, 'AC1: list events');
          browser.assert.ok(Array.isArray(body.events), 'AC1: events array');
          browser.assert.ok(body.events.length >= 1, 'AC1: at least one create event');
          const ev = body.events[0];
          browser.assert.equal(ev.serviceId, serviceId, 'AC1: serviceId echoed');
          browser.assert.equal(ev.eventType, 'create', 'AC1: create event');
          browser.assert.ok(ev.actor, 'AC1: actor present');
          browser.assert.ok(ev.createdAt, 'AC1: createdAt present');
        });
      });
    });

    // Unknown service_id -> 10301.
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/admin/deployments/events?service_id=99999999-9999-9999-9999-999999999999',
      org
    }, (res) => {
      api.assertBusinessError(browser, res, 10301, 'AC1: unknown service -> 10301');
    });
  },

  'AC2: scale records a field-level diff; event-type filter shows only matching events': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-dh-scale-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name, version: 'v1', weightPath: `models/${name}/v1/`}
    }, (res) => {
      const modelId = api.assertOk(browser, res, 'AC2: register model').modelId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/inference-services',
        org,
        body: {
          name: `svc-scale-${browser.globals.runId}-${browser.globals.testSeq}`,
          modelId,
          modelVersion: 'v1',
          imageId: 'img-vllm-nvidia-v063',
          replicas: '2',
          accelerator: 'nvidia',
          acceleratorType: 'gpu'
        }
      }, (res2) => {
        const serviceId = api.assertOk(browser, res2, 'AC2: create service').serviceId;

        // Scale to 4 replicas -> records a scale event with a diff.
        api.request(browser, {
          method: 'POST',
          path: `/api/v1/admin/inference-services/${serviceId}:scale`,
          org,
          body: {replicas: '4'}
        }, (res3) => {
          api.assertOk(browser, res3, 'AC2: scale service');

          api.request(browser, {
            method: 'GET',
            path: `/api/v1/admin/deployments/events?service_id=${serviceId}`,
            org
          }, (res4) => {
            const body = api.assertOk(browser, res4, 'AC2: list events after scale');
            browser.assert.ok(body.events.length >= 2, 'AC2: create + scale events');
            const scaleEv = body.events[0];
            browser.assert.equal(scaleEv.eventType, 'scale', 'AC2: newest is scale');
            browser.assert.ok(scaleEv.before.includes('2'), 'AC2: before has replicas 2');
            browser.assert.ok(scaleEv.after.includes('4'), 'AC2: after has replicas 4');
          });

          // Event-type filter shows only scale events.
          api.request(browser, {
            method: 'GET',
            path: `/api/v1/admin/deployments/events?service_id=${serviceId}&event_type=scale`,
            org
          }, (res5) => {
            const body = api.assertOk(browser, res5, 'AC2: filter by scale');
            browser.assert.ok(body.events.length >= 1, 'AC2: at least one scale event');
            for (const ev of body.events) {
              browser.assert.equal(ev.eventType, 'scale', 'AC2: all events are scale');
            }
          });
        });
      });
    });
  },

  // ---- Admin surface: rollback from history API (AC3) ----

  'AC3: RollbackDeployment reverts to the target event before, keeps service_id, records a rollback event; unknown event -> 10304': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-dh-roll-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name, version: 'v1', weightPath: `models/${name}/v1/`}
    }, (res) => {
      const modelId = api.assertOk(browser, res, 'AC3: register model').modelId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/inference-services',
        org,
        body: {
          name: `svc-roll-${browser.globals.runId}-${browser.globals.testSeq}`,
          modelId,
          modelVersion: 'v1',
          imageId: 'img-vllm-nvidia-v063',
          replicas: '2',
          accelerator: 'nvidia',
          acceleratorType: 'gpu'
        }
      }, (res2) => {
        const serviceId = api.assertOk(browser, res2, 'AC3: create service').serviceId;

        // Scale to 4 -> creates a scale event whose before is replicas 2.
        api.request(browser, {
          method: 'POST',
          path: `/api/v1/admin/inference-services/${serviceId}:scale`,
          org,
          body: {replicas: '4'}
        }, () => {
          api.request(browser, {
            method: 'GET',
            path: `/api/v1/admin/deployments/events?service_id=${serviceId}&event_type=scale`,
            org
          }, (res3) => {
            const body = api.assertOk(browser, res3, 'AC3: list scale events');
            const scaleEv = body.events[0];
            const scaleEventId = scaleEv.eventId;
            browser.assert.ok(Boolean(scaleEventId), 'AC3: scale eventId present');

            // Roll back to the scale event's before (replicas 2).
            api.request(browser, {
              method: 'POST',
              path: `/api/v1/admin/deployments/${serviceId}:rollback`,
              org,
              body: {eventId: scaleEventId}
            }, (res4) => {
              const rb = api.assertOk(browser, res4, 'AC3: rollback');
              browser.assert.equal(rb.serviceId, serviceId, 'AC3: service_id kept');
              browser.assert.equal(rb.state, 'deploying', 'AC3: transitions to deploying');

              // A new rollback event appears in the trail.
              api.request(browser, {
                method: 'GET',
                path: `/api/v1/admin/deployments/events?service_id=${serviceId}`,
                org
              }, (res5) => {
                const body = api.assertOk(browser, res5, 'AC3: list events after rollback');
                browser.assert.equal(body.events[0].eventType, 'rollback', 'AC3: newest is rollback');
              });
            });

            // Unknown event_id on a real service -> 10304. Use a valid-format
            // UUID so the event lookup is reached (a non-UUID event_id
            // returns 13 on PostgreSQL, the same class of issue the
            // developer documented for update-version's service_id).
            api.request(browser, {
              method: 'POST',
              path: `/api/v1/admin/deployments/${serviceId}:rollback`,
              org,
              body: {eventId: '99999999-9999-9999-9999-999999999999'}
            }, (res6) => {
              api.assertBusinessError(browser, res6, 10304, 'AC3: unknown event -> 10304');
            });
          });
        });
      });
    });

    // Unknown service_id -> 10301.
    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/deployments/99999999-9999-9999-9999-999999999999:rollback',
      org,
      body: {eventId: 'does-not-exist'}
    }, (res) => {
      api.assertBusinessError(browser, res, 10301, 'AC3: unknown service -> 10301');
    });
  },

  // ---- Surface separation (AC8) ----

  'AC8: a user-realm session calling the admin deployment prefix is rejected with 10038': function (browser) {
    browser.execute(function () {
      return localStorage.getItem('go-taas.user.session-token');
    }, [], (result) => {
      const token = result.value;
      browser.assert.ok(Boolean(token), 'AC8: user session token present');
      api.request(browser, {
        method: 'GET',
        path: '/api/v1/admin/deployments/events',
        org: browser.globals.orgA,
        headers: {Authorization: `Bearer ${token}`}
      }, (res) => {
        api.assertBusinessError(browser, res, 10038, 'AC8: user session on admin prefix -> 10038');
      });
    });
  },

  'AC8b: the admin deployment API is not reachable on the bare /api/v1/... prefix': function (browser) {
    const org = browser.globals.orgA;
    api.request(browser, {
      method: 'GET',
      path: '/api/v1/deployments/events',
      org
    }, (res) => {
      // Either a 404 (route not bound) or a user-surface business error;
      // it must NOT be the admin ListDeploymentEvents success envelope.
      browser.assert.ok(
        res.status === 404 || (res.body && res.body.code !== 0),
        'AC8b: admin deployment route not served on bare prefix'
      );
    });
  },

  // ---- Console pages: admin Deployment History page (AC4) ----

  'AC4: admin Deployment History page renders the filter bar and event trail table': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-dh-page-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name, version: 'v1', weightPath: `models/${name}/v1/`}
    }, (res) => {
      const modelId = api.assertOk(browser, res, 'AC4: register model').modelId;
      api.request(browser, {
        method: 'POST',
        path: '/api/v1/admin/inference-services',
        org,
        body: {
          name: `svc-page-${browser.globals.runId}-${browser.globals.testSeq}`,
          modelId,
          modelVersion: 'v1',
          imageId: 'img-vllm-nvidia-v063',
          replicas: '2',
          accelerator: 'nvidia',
          acceleratorType: 'gpu'
        }
      }, () => {
        browser.execute(`localStorage.setItem('go-taas.admin.org-id', '${org}')`);
        browser.url(browser.globals.baseUrl + '/admin/deployments');
        browser.waitForElementPresent('[data-testid="deploy-history-title"]', 15000, 'AC4: page renders');
        browser.waitForElementPresent('[data-testid="deploy-history-filter-bar"]', 10000, 'AC4: filter bar');
        browser.waitForElementPresent('[data-testid="deploy-history-service-select"]', 10000, 'AC4: service select');
        browser.waitForElementPresent('[data-testid="deploy-history-event-type-select"]', 10000, 'AC4: event type select');
        browser.waitForElementPresent('[data-testid="deploy-history-actor-input"]', 10000, 'AC4: actor input');
        browser.waitForElementPresent('[data-testid="deploy-history-range-select"]', 10000, 'AC4: range select');
        browser.waitForElementPresent('[data-testid="deploy-history-refresh"]', 10000, 'AC4: refresh button');
        // The trail table or empty state renders.
        browser.waitForElementPresent(
          '[data-testid="deploy-history-table"], [data-testid="deploy-history-empty"]',
          10000,
          'AC4: event trail table or empty state renders'
        );
      });
    });
  },

  // ---- Console pages: permission denied (AC9) ----

  'AC9: a session without the required role receives 10036 on the admin deployment API': function (browser) {
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
        path: '/api/v1/admin/deployments/events',
        org,
        headers: {Authorization: `Bearer ${token}`}
      }, (res) => {
        api.assertBusinessError(browser, res, 10036, 'AC9: non-member session on admin deployment API -> 10036');
      });
    });
  }
};