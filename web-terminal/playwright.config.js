import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir:'./test', testMatch:'browser.spec.js', timeout:60000, workers:1,
  outputDir:'./artifacts/results', reporter:'list',
  use:{baseURL:'http://127.0.0.1:4173',headless:true,viewport:{width:1440,height:1000}},
  webServer:{command:'node serve.mjs',url:'http://127.0.0.1:4173',reuseExistingServer:false,timeout:15000},
});
