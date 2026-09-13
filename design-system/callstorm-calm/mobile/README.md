# Native mobile adaptation

The mobile adapters are generated from `../tokens.json`. They are theme foundations, not a complete implementation of every visual component in the web reference.

## React Native

```tsx
import { View, Text, Pressable } from 'react-native';
import { colors, spacing, radii, typeStyles, buttonStyles } from './theme';

export function FocusCard({ onOpen }: { onOpen: () => void }) {
  return (
    <View style={{
      padding: spacing['24'], backgroundColor: colors.surface,
      borderColor: colors.border, borderWidth: 1, borderRadius: radii.card,
    }}>
      <Text style={[typeStyles.section, { color: colors.ink }]}>
        Your next step
      </Text>
      <Text style={[typeStyles.body, { color: colors['text-secondary'], marginTop: spacing['12'] }]}>
        Keep the most useful action close to the information it belongs to.
      </Text>
      <Pressable accessibilityRole="button" onPress={onOpen}
        style={({ pressed }) => [buttonStyles, {
          marginTop: spacing['20'],
          backgroundColor: pressed ? colors['action-hover'] : colors.action,
        }]}>
        <Text style={[typeStyles.label, { color: colors['on-action'] }]}>Open details</Text>
      </Pressable>
    </View>
  );
}
```

Use the default native font unless the project defines a licensed brand font. `typeStyles` leaves `fontFamily` unset for this reason. Apply a platform-appropriate monospace font only where an eyebrow or identifier benefits from it. Keep text scaling enabled. Give icon-only actions accessible labels.

## Flutter

Copy `callstorm_theme.dart` into the app and use:

```dart
MaterialApp(
  theme: callstormTheme(),
  home: const YourHomeScreen(),
)
```

Create a flat card explicitly, since the theme adapter does not override every Material component:

```dart
Container(
  padding: const EdgeInsets.all(CallstormSpace.s24),
  decoration: BoxDecoration(
    color: CallstormColors.surface,
    border: Border.all(color: CallstormColors.border),
    borderRadius: BorderRadius.circular(CallstormRadius.card),
  ),
  child: const Text('Your next step', style: CallstormType.section),
)
```

The theme starts from a Material light color scheme and overrides the central roles. Material components that are not explicitly themed can retain framework defaults. Extend their themes with the exported tokens as your app adds navigation bars, tabs, dialogs, or other controls. Do not assume that setting `ThemeData` alone reproduces every layout or component in the reference.

## Adaptation rules

- Use at least 48 logical-unit controls and comfortable space between adjacent actions.
- Use a 16-unit page inset and a single primary content column on phones.
- Preserve native back navigation, safe areas, keyboard avoidance, and text scaling.
- Choose a bottom bar for a few stable destinations, tabs for sibling content, and sheets or pushed screens for evidence.
- Keep headings around 28–33 depending on space; retain 16 body and 14 labels.
- Use progressive disclosure to reduce density; do not hide necessary task information behind unexplained icons.
- Draw charts with readable native-size labels and accessible alternatives. Pastel fills need strong outlines or direct labels when their boundaries carry meaning.
- Test these starter adapters against the project's installed SDK and actual devices. This export does not claim that a Flutter or React Native application was compiled or device-tested.
