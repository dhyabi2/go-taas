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

// Feature-13 (per-tenant model authorization) e2e suite, run against the
// compose stack gateway. Covers the acceptance criteria of
// docs/design/model-authorization.md reachable from the outside: the
// grant/revoke/list RPCs (AC1-AC4, AC10), the org-filtered model list
// (AC11), the deploy gate (AC5/AC6), and the restricted flag (AC13).

const api = require('../page-objects/api.js');

module.exports = {
  '@tags': ['model-authorization', 'feature-13'],

  beforeEach(browser) {
    browser.url(browser.globals.baseUrl + '/');
    browser.globals.testSeq = (browser.globals.testSeq || 0) + 1;
    browser.globals.orgA = `org-e2e-auth-${browser.globals.runId}-${browser.globals.testSeq}`;
    browser.globals.orgB = `org-e2e-authb-${browser.globals.runId}-${browser.globals.testSeq}`;
    api.ensureOrg(browser, browser.globals.orgA);
    api.ensureOrg(browser, browser.globals.orgB);
  },

  afterEach(browser) {
    browser.end();
  },

  'AC1/AC2/AC10/AC13: grant is stored and idempotent; model becomes restricted': function (browser) {
    const org = browser.globals.orgA;
    const name = `e2e-auth-model-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org,
      body: {name, version: 'v1', weight_path: `models/${name}/v1`}
    }, (res) => {
      const body = api.assertOk(browser, res, 'register model');
      const modelId = body.modelId;

      // Grant org-a.
      api.request(browser, {
        method: 'POST',
        path: `/api/v1/admin/models/${modelId}:grant`,
        org,
        body: {organization_id: org}
      }, (res2) => {
        api.assertOk(browser, res2, 'grant org-a');

        // Grant again: idempotent (AC2).
        api.request(browser, {
          method: 'POST',
          path: `/api/v1/admin/models/${modelId}:grant`,
          org,
          body: {organization_id: org}
        }, (res3) => {
          api.assertOk(browser, res3, 'grant org-a again (idempotent)');

          // List authorizations: one row (AC10).
          api.request(browser, {
            method: 'GET',
            path: `/api/v1/admin/models/${modelId}/authorizations`,
            org
          }, (res4) => {
            const body4 = api.assertOk(browser, res4, 'list authorizations');
            browser.assert.equal(body4.authorizations.length, 1, 'AC10: one grant row');
            browser.assert.equal(body4.authorizations[0].organizationId, org, 'AC10: granted org');

            // The model is now restricted (AC13).
            api.request(browser, {
              method: 'GET',
              path: '/api/v1/admin/models?page.limit=100',
              org
            }, (res5) => {
              const body5 = api.assertOk(browser, res5, 'list models');
              const m = body5.models.find((x) => x.modelId === modelId);
              browser.assert.ok(m, 'model present');
              browser.assert.equal(m.restricted, true, 'AC13: restricted flag true');
            });
          });
        });
      });
    });
  },

  'AC5/AC6: deploy gate blocks non-granted org, allows granted org': function (browser) {
    const orgA = browser.globals.orgA;
    const orgB = browser.globals.orgB;
    const name = `e2e-auth-deploy-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org: orgA,
      body: {name, version: 'v1', weight_path: `models/${name}/v1`}
    }, (res) => {
      const body = api.assertOk(browser, res, 'register model');
      const modelId = body.modelId;

      // Grant org-a only.
      api.request(browser, {
        method: 'POST',
        path: `/api/v1/admin/models/${modelId}:grant`,
        org: orgA,
        body: {organization_id: orgA}
      }, (res2) => {
        api.assertOk(browser, res2, 'grant org-a');

        // org-b (not granted) deploy → 10105 (AC5).
        api.request(browser, {
          method: 'POST',
          path: '/api/v1/admin/inference-services',
          org: orgB,
          body: {name: `svc-${browser.globals.testSeq}`, model_id: modelId, model_version: 'v1', image_id: 'img-vllm-nvidia-v063', replicas: 1, accelerator: 'nvidia'}
        }, (res3) => {
          api.assertBusinessError(browser, res3, 10105, 'AC5: non-granted org blocked');

          // org-a (granted) deploy → success (AC6).
          api.request(browser, {
            method: 'POST',
            path: '/api/v1/admin/inference-services',
            org: orgA,
            body: {name: `svc-ok-${browser.globals.testSeq}`, model_id: modelId, model_version: 'v1', image_id: 'img-vllm-nvidia-v063', replicas: 1, accelerator: 'nvidia'}
          }, (res4) => {
            api.assertOk(browser, res4, 'AC6: granted org deploys');
          });
        });
      });
    });
  },

  'AC11: org-filtered model list hides restricted models from non-granted orgs': function (browser) {
    const orgA = browser.globals.orgA;
    const orgB = browser.globals.orgB;
    const name = `e2e-auth-filter-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org: orgA,
      body: {name, version: 'v1', weight_path: `models/${name}/v1`}
    }, (res) => {
      const body = api.assertOk(browser, res, 'register model');
      const modelId = body.modelId;

      // Grant org-a.
      api.request(browser, {
        method: 'POST',
        path: `/api/v1/admin/models/${modelId}:grant`,
        org: orgA,
        body: {organization_id: orgA}
      }, (res2) => {
        api.assertOk(browser, res2, 'grant org-a');

        // org-a sees the model.
        api.request(browser, {
          method: 'GET',
          path: `/api/v1/admin/models?organization_id=${orgA}&page.limit=100`,
          org: orgA
        }, (res3) => {
          const body3 = api.assertOk(browser, res3, 'org-a filtered list');
          const m = body3.models.find((x) => x.modelId === modelId);
          browser.assert.ok(m, 'AC11: granted org sees the model');

          // org-b does not see the model.
          api.request(browser, {
            method: 'GET',
            path: `/api/v1/admin/models?organization_id=${orgB}&page.limit=100`,
            org: orgB
          }, (res4) => {
            const body4 = api.assertOk(browser, res4, 'org-b filtered list');
            const m2 = body4.models.find((x) => x.modelId === modelId);
            browser.assert.ok(!m2, 'AC11: non-granted org does not see the model');
          });
        });
      });
    });
  },

  'AC3/AC4: revoke removes the grant and returns the model to default-allow': function (browser) {
    const orgA = browser.globals.orgA;
    const name = `e2e-auth-revoke-${browser.globals.runId}-${browser.globals.testSeq}`;

    api.request(browser, {
      method: 'POST',
      path: '/api/v1/admin/models',
      org: orgA,
      body: {name, version: 'v1', weight_path: `models/${name}/v1`}
    }, (res) => {
      const body = api.assertOk(browser, res, 'register model');
      const modelId = body.modelId;

      // Grant then revoke.
      api.request(browser, {
        method: 'POST',
        path: `/api/v1/admin/models/${modelId}:grant`,
        org: orgA,
        body: {organization_id: orgA}
      }, (res2) => {
        api.assertOk(browser, res2, 'grant org-a');
        api.request(browser, {
          method: 'POST',
          path: `/api/v1/admin/models/${modelId}:revoke`,
          org: orgA,
          body: {organization_id: orgA}
        }, (res3) => {
          api.assertOk(browser, res3, 'revoke org-a');

          // Revoking a non-granted org is a no-op (AC4).
          api.request(browser, {
            method: 'POST',
            path: `/api/v1/admin/models/${modelId}:revoke`,
            org: orgA,
            body: {organization_id: orgA}
          }, (res4) => {
            api.assertOk(browser, res4, 'revoke again (idempotent)');

            // Model is back to default-allow (AC3).
            api.request(browser, {
              method: 'GET',
              path: '/api/v1/admin/models?page.limit=100',
              org: orgA
            }, (res5) => {
              const body5 = api.assertOk(browser, res5, 'list models');
              const m = body5.models.find((x) => x.modelId === modelId);
              browser.assert.equal(m.restricted, false, 'AC3: model back to default-allow');
            });
          });
        });
      });
    });
  }
};
