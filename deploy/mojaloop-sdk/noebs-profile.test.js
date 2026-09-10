'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { guardPrepare, guardNativePrepare } = require('./noebs-profile');

test('prepare expiry is the agreed immutable quote expiry and cannot exceed it', () => {
    const q = { expiration: '2030-01-01T00:00:00Z', ilpPacket: 'packet', condition: 'condition', transferAmount: { amount: '1.00', currency: 'SDG' } };
    const p = { transferId: 'transfer', expiration: q.expiration, ilpPacket: q.ilpPacket, condition: q.condition, amount: q.transferAmount };
    const state = { transferId: p.transferId, quoteResponse: { body: q } };
    guardPrepare(state, p, 0);
    assert.throws(() => guardPrepare(state, {...p, expiration:'2030-01-01T00:00:01Z'},0));
    assert.throws(() => guardPrepare(state, p, Date.parse(q.expiration)));
    state.noebsPrepare = structuredClone(p);
    guardPrepare(state, p, 0);
    assert.throws(() => guardPrepare(state, {...p, payeeFsp:'changed'},0));
    assert.throws(() => guardPrepare(state, {...p, amount:{amount:'2.00',currency:'SDG'}},0));
});

test('native send preserves valid decimal forms and fences expired or invalid preparations', () => {
    const p = { transferId:'00000000-0000-4000-8000-000000000001',payerFsp:'noebs',payeeFsp:'bankone',amount:{amount:'1',currency:'SDG'},expiration:'2030-01-01T00:00:00Z',ilpPacket:'packet',condition:'condition' };
    for (const amount of ['1','1.0','1.00','0.01','92233720368547758.07']) {
        const exact = {...p,amount:{...p.amount,amount}};
        const before = JSON.stringify(exact);
        guardNativePrepare(exact,'bankone',0);
        assert.equal(JSON.stringify(exact),before);
    }
    for (const amount of ['0','-1','1.001','1e2','01.00','92233720368547758.08']) {
        assert.throws(()=>guardNativePrepare({...p,amount:{...p.amount,amount}},'bankone',0));
    }
    assert.throws(()=>guardNativePrepare(p,'walletone',0));
    assert.throws(()=>guardNativePrepare(p,'bankone',Date.parse(p.expiration)));
    assert.throws(()=>guardNativePrepare({...p,payerFsp:'walletone'},'bankone',0));
});
