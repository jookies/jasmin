import {
  BaseEdge,
  EdgeLabelRenderer,
  Position,
  getSmoothStepPath,
  type Edge,
  type EdgeProps,
} from "@xyflow/react";

export type TopologyFlowEdgeData = {
  /** Side of the stroke where the label sits, so it never erases the line. */
  labelSide: -1 | 1;
  /** Places adjacent elbows one after another instead of stacking them. */
  stepPosition: number;
  /** Absolute y-coordinate for a skip-column route around intervening cards. */
  detourY?: number;
};

export type TopologyFlowEdgeType = Edge<TopologyFlowEdgeData, "topologyFlow">;

const LABEL_GAP = 17;
const TERMINAL_APPROACH = 22;
const CORNER_RADIUS = 8;

const isHorizontalHandle = (position: Position) =>
  position === Position.Left || position === Position.Right;

type PathPoint = { x: number; y: number };

const compactOrthogonalPoints = (points: PathPoint[]): PathPoint[] => {
  const unique = points.filter(
    (point, index) =>
      index === 0 || point.x !== points[index - 1].x || point.y !== points[index - 1].y,
  );
  return unique.filter((point, index) => {
    if (index === 0 || index === unique.length - 1) return true;
    const previous = unique[index - 1];
    const next = unique[index + 1];
    return !(
      (previous.x === point.x && point.x === next.x) ||
      (previous.y === point.y && point.y === next.y)
    );
  });
};

const roundedOrthogonalPath = (rawPoints: PathPoint[]): string => {
  const points = compactOrthogonalPoints(rawPoints);
  if (points.length < 2) return "";
  const commands = [`M ${points[0].x},${points[0].y}`];

  for (let index = 1; index < points.length - 1; index += 1) {
    const previous = points[index - 1];
    const point = points[index];
    const next = points[index + 1];
    const incoming = Math.abs(point.x - previous.x) + Math.abs(point.y - previous.y);
    const outgoing = Math.abs(next.x - point.x) + Math.abs(next.y - point.y);
    const radius = Math.min(CORNER_RADIUS, incoming / 2, outgoing / 2);
    const before: PathPoint = {
      x: point.x + Math.sign(previous.x - point.x) * radius,
      y: point.y + Math.sign(previous.y - point.y) * radius,
    };
    const after: PathPoint = {
      x: point.x + Math.sign(next.x - point.x) * radius,
      y: point.y + Math.sign(next.y - point.y) * radius,
    };
    commands.push(`L ${before.x},${before.y}`, `Q ${point.x},${point.y} ${after.x},${after.y}`);
  }

  const last = points[points.length - 1];
  commands.push(`L ${last.x},${last.y}`);
  return commands.join(" ");
};

const longestHorizontalSegmentCentre = (rawPoints: PathPoint[]): PathPoint => {
  const points = compactOrthogonalPoints(rawPoints);
  let longest: { length: number; centre: PathPoint } | undefined;
  for (let index = 1; index < points.length; index += 1) {
    const previous = points[index - 1];
    const point = points[index];
    if (previous.y !== point.y) continue;
    const length = Math.abs(point.x - previous.x);
    if (!longest || length > longest.length) {
      longest = {
        length,
        centre: { x: (previous.x + point.x) / 2, y: point.y },
      };
    }
  }
  return longest?.centre ?? {
    x: (points[0].x + points[points.length - 1].x) / 2,
    y: (points[0].y + points[points.length - 1].y) / 2,
  };
};

export const horizontalDetourGeometry = ({
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  detourY,
}: {
  sourceX: number;
  sourceY: number;
  targetX: number;
  targetY: number;
  sourcePosition: Position;
  targetPosition: Position;
  detourY: number;
}): { path: string; label: PathPoint; points: PathPoint[] } => {
  const sourceDirection = sourcePosition === Position.Left ? -1 : 1;
  const targetDirection = targetPosition === Position.Left ? -1 : 1;
  const points = compactOrthogonalPoints([
    { x: sourceX, y: sourceY },
    { x: sourceX + sourceDirection * TERMINAL_APPROACH, y: sourceY },
    { x: sourceX + sourceDirection * TERMINAL_APPROACH, y: detourY },
    { x: targetX + targetDirection * TERMINAL_APPROACH, y: detourY },
    { x: targetX + targetDirection * TERMINAL_APPROACH, y: targetY },
    { x: targetX, y: targetY },
  ]);
  return {
    path: roundedOrthogonalPath(points),
    label: longestHorizontalSegmentCentre(points),
    points,
  };
};

/**
 * A native smooth-step relationship with a label that sits beside its stroke.
 * Card ports separate crowded endpoints; the graph library keeps every bend
 * orthogonal and guarantees that the last segment meets the arrow correctly.
 */
export const TopologyFlowEdge = ({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerStart,
  markerEnd,
  style,
  label,
  labelStyle,
  interactionWidth,
  data,
}: EdgeProps<TopologyFlowEdgeType>) => {
  const labelSide = data?.labelSide ?? -1;
  const horizontal = isHorizontalHandle(sourcePosition) && isHorizontalHandle(targetPosition);
  const detour =
    horizontal && data?.detourY !== undefined
      ? horizontalDetourGeometry({
          sourceX,
          sourceY,
          targetX,
          targetY,
          sourcePosition,
          targetPosition,
          detourY: data.detourY,
        })
      : undefined;
  const [smoothPath, smoothLabelX, smoothLabelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: CORNER_RADIUS,
    offset: TERMINAL_APPROACH,
    stepPosition: data?.stepPosition ?? 0.5,
  });
  const path = detour?.path ?? smoothPath;
  const pathLabelX = detour?.label.x ?? smoothLabelX;
  const pathLabelY = detour?.label.y ?? smoothLabelY;
  const labelX = pathLabelX + (horizontal ? 0 : labelSide * LABEL_GAP);
  const labelY = pathLabelY + (horizontal ? labelSide * LABEL_GAP : 0);

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerStart={markerStart}
        markerEnd={markerEnd}
        style={style}
        interactionWidth={interactionWidth}
      />
      {label ? (
        <EdgeLabelRenderer>
          <div
            className="topology-edge-label nodrag nopan"
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              ...labelStyle,
            }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  );
};
