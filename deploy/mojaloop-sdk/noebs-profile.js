'use strict';

// Local, versioned profile for official SDK 24.19.5. This fixes the dispatch
// expiry/replay boundary; it is not an upstream Mojaloop feature claim.
const { isDeepStrictEqual } = require('node:util');

function guardPrepare(data, prepare, now = Date.now()) {
    const quote = data.quoteResponse?.body;
    if (!quote || !Number.isFinite(Date.parse(quote.expiration)) ||
        prepare.expiration !== quote.expiration || Date.parse(prepare.expiration) <= now ||
        prepare.transferId !== data.transferId ||
        prepare.ilpPacket !== quote.ilpPacket || prepare.condition !== quote.condition ||
        !isDeepStrictEqual(prepare.amount, quote.transferAmount)) {
        throw new Error('noebs: invalid or expired immutable prepare');
    }
    if (data.noebsPrepare && !isDeepStrictEqual(data.noebsPrepare, prepare)) {
        throw new Error('noebs: prepare replay changed');
    }
}

function guardNativePrepare(prepare, fspId, now = Date.now()) {
    if (!prepare || prepare.payerFsp !== 'noebs' || prepare.payeeFsp !== fspId || fspId === 'noebs' ||
        !/^[0-9a-f-]{36}$/.test(prepare.transferId) || prepare.amount?.currency !== 'SDG' ||
        !/^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$/.test(prepare.amount.amount) ||
        !Number.isFinite(Date.parse(prepare.expiration)) || Date.parse(prepare.expiration) <= now ||
        !prepare.ilpPacket || !prepare.condition) {
        throw new Error('noebs: invalid or expired native prepare');
    }
    const [whole, fraction = ''] = prepare.amount.amount.split('.');
    const minor = BigInt(whole + (fraction + '00').slice(0, 2));
    if (minor <= 0n || minor > 9223372036854775807n) {
        throw new Error('noebs: native amount outside signed minor-unit range');
    }
}

module.exports = { guardPrepare, guardNativePrepare };
