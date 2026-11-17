package io.nekohasekai.sfa.flutter;

import android.app.Activity;
import android.app.Instrumentation;
import android.app.UiAutomation;
import android.graphics.Rect;
import android.os.Bundle;
import android.os.SystemClock;
import android.util.Log;
import android.view.InputDevice;
import android.view.MotionEvent;
import android.view.accessibility.AccessibilityNodeInfo;

/** CI-only finger gestures. Java avoids dependencies supplied only by the main APK. */
public final class SmokeGestureInstrumentation extends Instrumentation {
    private static final String APP_PACKAGE = "io.ericlee.bfa";
    private static final String TEST_PACKAGE = APP_PACKAGE + ".test";
    private Bundle gestureArguments;

    @Override
    public void onCreate(Bundle arguments) {
        super.onCreate(arguments);
        gestureArguments = arguments == null ? new Bundle() : new Bundle(arguments);
        start();
    }

    @Override
    public void onStart() {
        Bundle result = new Bundle();
        int resultCode = Activity.RESULT_CANCELED;
        try {
            if (!TEST_PACKAGE.equals(getContext().getPackageName())
                    || !TEST_PACKAGE.equals(getTargetContext().getPackageName())) {
                throw new IllegalStateException("Gesture instrumentation must target its own test APK");
            }
            int x = argument("x", 1, 16384);
            int y = argument("y", 1, 16384);
            int duration = argument("durationMs", 500, 5000);
            UiAutomation automation = getUiAutomation();
            requireAppWindow(automation, x, y);
            long downTime = SystemClock.uptimeMillis();
            boolean pressed = false;
            try {
                pressed = true;
                if (!inject(automation, downTime, MotionEvent.ACTION_DOWN, x, y)) {
                    throw new IllegalStateException("Finger DOWN injection failed");
                }
                SystemClock.sleep(duration);
                if (!inject(automation, downTime, MotionEvent.ACTION_UP, x, y)) {
                    throw new IllegalStateException("Finger UP injection failed");
                }
                pressed = false;
            } finally {
                if (pressed) {
                    inject(automation, downTime, MotionEvent.ACTION_CANCEL, x, y);
                }
            }
            result.putString("bbox_gesture_ok", "true");
            result.putString("bbox_gesture", "finger-long-press");
            result.putInt("x", x);
            result.putInt("y", y);
            result.putInt("durationMs", duration);
            resultCode = Activity.RESULT_OK;
        } catch (Throwable error) {
            result.putString("bbox_gesture_ok", "false");
            result.putString("bbox_gesture_error", error.toString());
            Log.e("BboxSmokeGesture", "Finger injection failed", error);
        } finally {
            finish(resultCode, result);
        }
    }

    private int argument(String name, int minimum, int maximum) {
        int value = Integer.parseInt(gestureArguments.getString(name));
        if (value < minimum || value > maximum) {
            throw new IllegalArgumentException(name + " must be between " + minimum + " and " + maximum);
        }
        return value;
    }

    @SuppressWarnings("deprecation")
    private void requireAppWindow(UiAutomation automation, int x, int y) {
        long deadline = SystemClock.uptimeMillis() + 5000;
        while (SystemClock.uptimeMillis() < deadline) {
            AccessibilityNodeInfo root = automation.getRootInActiveWindow();
            if (root == null) {
                SystemClock.sleep(100);
                continue;
            }
            try {
                CharSequence packageName = root.getPackageName();
                if (packageName == null || !APP_PACKAGE.contentEquals(packageName)) {
                    throw new IllegalStateException("B-box must be the active window, found " + packageName);
                }
                Rect bounds = new Rect();
                root.getBoundsInScreen(bounds);
                if (!bounds.contains(x, y)) {
                    throw new IllegalArgumentException("Gesture (" + x + ", " + y + ") is outside " + bounds);
                }
                return;
            } finally {
                root.recycle();
            }
        }
        throw new IllegalStateException("No active B-box window for the gesture");
    }

    private boolean inject(UiAutomation automation, long downTime, int action, int x, int y) {
        MotionEvent.PointerProperties properties = new MotionEvent.PointerProperties();
        properties.id = 0;
        properties.toolType = MotionEvent.TOOL_TYPE_FINGER;
        MotionEvent.PointerCoords coordinates = new MotionEvent.PointerCoords();
        coordinates.x = x;
        coordinates.y = y;
        coordinates.pressure = action == MotionEvent.ACTION_DOWN ? 1f : 0f;
        coordinates.size = 1f;
        MotionEvent event = MotionEvent.obtain(
                downTime, SystemClock.uptimeMillis(), action, 1,
                new MotionEvent.PointerProperties[] {properties},
                new MotionEvent.PointerCoords[] {coordinates},
                0, 0, 1f, 1f, 0, 0, InputDevice.SOURCE_TOUCHSCREEN, 0);
        try {
            return automation.injectInputEvent(event, true);
        } finally {
            event.recycle();
        }
    }
}
