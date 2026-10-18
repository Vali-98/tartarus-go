'use strict';

importScripts('scan-data.js', 'pick-data.js', 'scan.js');

let cache = {};

self.onmessage = (event) => {
  const { width, height, data, id, frameID, kind } = event.data;
  try {
    const image = { width, height, data: new Uint8ClampedArray(data) };
    let result;
    let detectedKind = kind;
    if ((kind === 'manual' || kind === 'treasure') && self.TartarusScan.hasTreasureSelector(image)) {
      result = { ok: false, treasureSelector: true, reason: "Warden's Treasure selector detected." };
      detectedKind = 'treasure';
    } else if (kind === 'instruments' || kind === 'manual') {
      const pick = self.TartarusScan.scanPick(image, self.TARTARUS_SCAN, self.TARTARUS_PICK);
      const confidences = pick.cards ? pick.cards.map((card) => card ? card.conf : 0).sort((a, b) => b - a) : [];
      if (kind === 'instruments' || pick.ok && confidences[1] >= 0.5) {
        result = pick;
        detectedKind = 'instruments';
      } else if (self.TartarusScan.hasTreasureSelector(image)) {
        result = { ok: false, treasureSelector: true, reason: "Warden's Treasure selector detected." };
        detectedKind = 'treasure';
      } else {
        result = self.TartarusScan.scan(image, self.TARTARUS_SCAN, { cache });
      }
    } else {
      result = self.TartarusScan.scan(image, self.TARTARUS_SCAN, { cache });
      if (!result.ok) {
        if (kind !== 'instruments' && self.TartarusScan.hasTreasureSelector(image)) {
          result = { ok: false, treasureSelector: true, reason: "Warden's Treasure selector detected." };
          detectedKind = 'treasure';
        } else {
          const pick = self.TartarusScan.scanPick(image, self.TARTARUS_SCAN, self.TARTARUS_PICK);
          const confidences = pick.cards ? pick.cards.map((card) => card ? card.conf : 0).sort((a, b) => b - a) : [];
          if (pick.ok && confidences[1] >= 0.5) {
            result = pick;
            detectedKind = 'instruments';
          }
        }
      }
    }
    self.postMessage({ id, frameID, kind: detectedKind, result });
  } catch (error) {
    self.postMessage({ id, frameID, kind, error: String(error && error.message || error) });
  }
};
