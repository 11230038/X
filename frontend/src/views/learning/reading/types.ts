/** 阅读页内部共享的状态形状。 */

import type { ApiFailure } from '../../../api/client'
import type { ReadingUnit } from '../../../api/reading'

export type ReadingStatus = 'idle' | 'loading' | 'ready' | 'failed'

export interface UnitState {
  status: 'loading' | 'ready' | 'failed'
  text: string
  failure: ApiFailure | null
}

export interface UnitEntry {
  unit: ReadingUnit
  state: UnitState | null
}
