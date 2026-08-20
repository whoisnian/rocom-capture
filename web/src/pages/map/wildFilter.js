// 图层 = 一个开关,可覆盖后端 kinds 里的多个类别(异色与炫彩合成一个开关)。
// standalone 项独立附加显示,其余项目可分别加入 OR 与 AND 条件集。
export const WILD_LAYERS = [
  { k: 'mutation', group: 'mutation', n: '异色/炫彩', kinds: ['shiny', 'colorful'], color: '#7ad3ff', priority: 100, on: true, standalone: true },
  { k: 'pollution', group: 'pollution', n: '污染', kinds: ['pollution'], color: '#c792ea', priority: 90, standalone: true },
  { k: 'weight-big', group: 'weight', n: '大块头', kinds: ['weight-big'], color: '#f5b942', priority: 40 },
  { k: 'weight-small', group: 'weight', n: '小不点', kinds: ['weight-small'], color: '#4c8dff', priority: 40 },
  { k: 'weight-big-max', group: 'weight', n: '大块头MAX', kinds: ['weight-big-max'], color: '#ff6b57', priority: 80 },
  { k: 'weight-small-max', group: 'weight', n: '小不点MAX', kinds: ['weight-small-max'], color: '#2dd4bf', priority: 80 },
  { k: 'voice-high', group: 'voice', n: '婉转声', kinds: ['voice-high'], color: '#e6d05f', priority: 30 },
  { k: 'voice-low', group: 'voice', n: '粗嗓门', kinds: ['voice-low'], color: '#8b9cff', priority: 30 },
  { k: 'voice-high-max', group: 'voice', n: '婉转声MAX', kinds: ['voice-high-max'], color: '#ff7eb6', priority: 70 },
  { k: 'voice-low-max', group: 'voice', n: '粗嗓门MAX', kinds: ['voice-low-max'], color: '#55d6e8', priority: 70 },
]

export function matchesWildPet(pet, standalone, orLayers, andLayers) {
  const hits = (layer) => layer.kinds.some((kind) => (pet.kinds || []).includes(kind))
  if (standalone.some(hits)) return true
  if (orLayers.some(hits)) return true

  const grouped = new Map()
  andLayers.forEach((layer) => grouped.set(
    layer.group, [...(grouped.get(layer.group) || []), layer]))
  const groups = [...grouped.values()]
  return groups.length > 0 && groups.every((group) => group.some(hits))
}
