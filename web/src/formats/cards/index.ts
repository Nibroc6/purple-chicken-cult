// Cards (default) format (SCAFFOLD contract file).
import type { FormatModule } from '../types';
import { FeedLayout } from './FeedLayout';
import { PostLayout } from './PostLayout';
import { TreeLayout } from './TreeLayout';

const format: FormatModule = { id: 'cards', FeedLayout, PostLayout, TreeLayout };
export default format;
