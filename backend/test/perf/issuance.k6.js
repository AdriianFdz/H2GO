import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';

const createLatency = new Trend('create_issuance_latency', true);
const rejectLatency = new Trend('reject_issuance_latency', true);
const errorRate     = new Rate('errors');
const txCount       = new Counter('transactions_completed');

const BASE_URL    = __ENV.BASE_URL || 'http://localhost:3003';
const PRODUCER_ID = 'e5f2d906-fd68-4f8e-ac40-0b01f6b52fe1';

export const options = {
  stages: [
    { duration: '15s', target: 5  },
    { duration: '30s', target: 20 },
    { duration: '10s', target: 0  },
  ],
  thresholds: {
    create_issuance_latency: ['p(100)<7000'],
    reject_issuance_latency: ['p(100)<7000'],
    http_req_failed:         ['rate<0.05'],
    errors:                  ['rate<0.05'],
  },
};

function login(email, password) {
  const res = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ email, password }),
    { headers: { 'Content-Type': 'application/json' } },
  );

  check(res, { [`login ${email} ok`]: (r) => r.status === 200 || r.status === 201 });
  if (res.status !== 200 && res.status !== 201) throw new Error(`Login failed (${email}): ${res.body}`);

  const cookie = res.cookies['token']?.[0]?.value;
  if (!cookie) throw new Error(`No token cookie for ${email}`);

  return cookie;
}

export function setup() {
  return {
    traderToken: login('trader@test.com', '123456'),
    cnmcToken:   login('cnmc@test.com',   '123456'),
  };
}

export default function (data) {
  const headers = { 'Content-Type': 'application/json' };

  // POST /requests/issuance
  const traderJar = http.cookieJar();
  traderJar.set(BASE_URL, 'token', data.traderToken);

  const createBody = JSON.stringify({
    producerId: PRODUCER_ID,
    assetType:  'ELECTRICITY',
    amount:     Math.floor(Math.random() * 1000) + 1,
  });

  const t0 = Date.now();
  const createRes = http.post(
    `${BASE_URL}/requests/issuance`,
    createBody,
    { headers, jar: traderJar },
  );
  createLatency.add(Date.now() - t0);

  const createOk = check(createRes, {
    'POST /issuance 201':  (r) => r.status === 201,
    'devuelve requestId':  (r) => JSON.parse(r.body)?.requestId !== undefined,
  });
  errorRate.add(!createOk);

  if (!createOk) {
    console.error(`Create failed [VU=${__VU}]: ${createRes.status} ${createRes.body}`);
    sleep(1);
    return;
  }

  const requestId = JSON.parse(createRes.body).requestId;

  sleep(0.5);

  // POST /requests/issuance/:id/reject
  const cnmcJar = http.cookieJar();
  cnmcJar.set(BASE_URL, 'token', data.cnmcToken);

  const rejectBody = JSON.stringify({ comment: `perf-reject-vu${__VU}-iter${__ITER}` });

  const t1 = Date.now();
  const rejectRes = http.post(
    `${BASE_URL}/requests/issuance/${requestId}/reject`,
    rejectBody,
    { headers, jar: cnmcJar },
  );
  rejectLatency.add(Date.now() - t1);

  const rejectOk = check(rejectRes, {
    'POST /reject ok': (r) => r.status === 200 || r.status === 201,
  });
  errorRate.add(!rejectOk);

  if (!rejectOk) {
    console.error(`Reject failed [VU=${__VU}]: ${rejectRes.status} ${rejectRes.body}`);
  }

  if (createOk && rejectOk) txCount.add(1);

  sleep(1);
}

export function handleSummary(data) {
  return {
    'test/perf/results/issuance.json': JSON.stringify(data, null, 2),
  };
}