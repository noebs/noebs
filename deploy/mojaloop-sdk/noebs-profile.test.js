'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { guardPrepare } = require('./noebs-profile');

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
