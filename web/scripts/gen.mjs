// Runs the generators used by the pre* npm hooks: `node scripts/gen.mjs [api|pseudo]`
// (no argument = both). Outputs are gitignored and written atomically.
import { generateAPI } from './openapi-merge.mjs';
import { generatePseudo } from './pseudo-locale.mjs';

const what = process.argv[2] ?? 'all';
if (what === 'all' || what === 'api') await generateAPI();
if (what === 'all' || what === 'pseudo') await generatePseudo();
