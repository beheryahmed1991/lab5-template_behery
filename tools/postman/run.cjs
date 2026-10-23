'use strict';
const fs = require('node:fs');
const path = require('node:path');

function prepare(variables, variant = 'v2') {
  if (variant !== 'v2') throw new Error('This project uses instructor variant v2');
  for (const name of ['AUTH0_CLIENT_ID', 'AUTH0_CLIENT_SECRET', 'AUTH0_TEST_USERNAME',
    'AUTH0_TEST_PASSWORD', 'AUTH0_ISSUER', 'AUTH0_AUDIENCE', 'LAB5_SERVICE_URL']) {
    if (!variables[name] || !variables[name].trim()) {
      throw new Error('Missing ' + name + '. Configure the GitHub Secret or environment variable.');
    }
  }
  if (new URL(variables.AUTH0_ISSUER).protocol !== 'https:') throw new Error('AUTH0_ISSUER must use HTTPS');
  if (!['http:', 'https:'].includes(new URL(variables.LAB5_SERVICE_URL).protocol)) throw new Error('Invalid service URL');
  const root = path.resolve(__dirname, '../..');
  const read = (name) => JSON.parse(fs.readFileSync(path.join(root, variant, 'postman', name), 'utf8'));
  const collection = read('collection.json');
  const environment = read('environment.json');
  const tokenRequests = [];
  function visit(items) {
    for (const item of items) {
      if (item.item) visit(item.item);
      if (item.name === '[auth] Get Token') tokenRequests.push(item);
    }
  }
  visit(collection.item);
  if (tokenRequests.length !== 1) throw new Error('Expected one instructor token request');
  const body = tokenRequests[0].request.body;
  if (body.mode !== 'urlencoded') throw new Error('Unexpected token request format');
  for (const field of body.urlencoded) {
    if (field.key === 'clientId') field.key = 'client_id';
    if (field.key === 'clientSecret') field.key = 'client_secret';
  }
  body.urlencoded = body.urlencoded.filter((field) => field.key !== 'audience');
  body.urlencoded.push({ key: 'audience', value: variables.AUTH0_AUDIENCE, type: 'text' });
  const values = {
    serviceUrl: variables.LAB5_SERVICE_URL.replace(/\/$/, ''),
    identityProviderUrl: variables.AUTH0_ISSUER.replace(/\/$/, ''),
    username: variables.AUTH0_TEST_USERNAME,
    password: variables.AUTH0_TEST_PASSWORD,
    clientId: variables.AUTH0_CLIENT_ID,
    clientSecret: variables.AUTH0_CLIENT_SECRET,
  };
  for (const [key, value] of Object.entries(values)) {
    const previous = environment.values.find((entry) => entry.key === key);
    if (previous) Object.assign(previous, { value, enabled: true });
    else environment.values.push({ key, value, enabled: true });
  }
  return { collection, environment };
}

function main() {
  let config;
  try {
    config = prepare(process.env, process.argv[2] || 'v2');
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
    return;
  }
  if (process.argv.includes('--check')) {
    console.log('Auth0 variables are present; instructor files loaded.');
    return;
  }
  const newman = require('newman');
  // Use Newman's standard CLI report, including its executed/failed summary table.
  newman.run({ ...config, reporters: ['cli'], delayRequest: 100, timeoutRequest: 30000,
    timeoutScript: 10000, insecure: false }, (error, summary) => {
    if (error) {
      console.error('Newman could not complete the run.');
      process.exitCode = 1;
      return;
    }
    const stats = summary.run.stats;
    process.exitCode = summary.run.failures.length || !stats.requests.total || !stats.assertions.total ? 1 : 0;
  });
}
module.exports = { prepare };
if (require.main === module) main();
