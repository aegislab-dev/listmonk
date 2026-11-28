const { defineConfig } = require('cypress');

module.exports = defineConfig({
  // Cypress.env() is deprecated since Cypress 15.10.0 and every run warns unless it is closed here.
  // Specs read public values with Cypress.expose() and the login with cy.env().
  // Cypress 16.0 removes both Cypress.env() and this option: drop the line when upgrading.
  allowCypressEnv: false,
  // Public values. Cypress puts CYPRESS_<name> variables only into `env`, so the two that CI and
  // scripts/axm/axm pass (CYPRESS_mailpitUrl, CYPRESS_apiUrl) are copied here.
  expose: {
    apiUrl: process.env.CYPRESS_apiUrl || 'http://localhost:9000',
    mailpitUrl: process.env.CYPRESS_mailpitUrl || '',
  },
  env: {
    serverInitCmd:
      'pkill -9 listmonk; cd ../ && LISTMONK_ADMIN_USER=admin LISTMONK_ADMIN_PASSWORD=listmonk ./listmonk --install --yes && setsid ./listmonk </dev/null >/dev/null 2>&1 &',
    serverInitBlankCmd:
      'pkill -9 listmonk; cd ../ && ./listmonk --install --yes && setsid ./listmonk </dev/null >/dev/null 2>&1 &',
    LISTMONK_ADMIN_USER: 'admin',
    LISTMONK_ADMIN_PASSWORD: 'listmonk',
  },
  viewportWidth: 1400,
  viewportHeight: 950,
  e2e: {
    experimentalRunAllSpecs: true,
    testIsolation: false,
    // We've imported your old cypress plugins here.
    // You may want to clean this up later by importing these.
    setupNodeEvents(on, config) {
      return require('./cypress/plugins/index.js')(on, config);
    },
    baseUrl: 'http://localhost:9000',
  },
});
