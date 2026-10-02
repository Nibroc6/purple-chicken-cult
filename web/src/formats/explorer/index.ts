// Explorer (Phase 1) format (SCAFFOLD contract file).
import type { FormatModule } from '../types';
import { FeedLayout } from './FeedLayout';
import { PostLayout } from './PostLayout';
import { TreeLayout } from './TreeLayout';

const format: FormatModule = { id: 'explorer', FeedLayout, PostLayout, TreeLayout };
export default format;
