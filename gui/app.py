#!/usr/bin/env python3
"""
Sticker Search GUI - Desktop app for quick sticker search and send
Uses Telegram User API (Pyrogram) to send stickers directly
"""
import sys
import os
import signal
import asyncio
import threading
import json
import subprocess
import re
import hashlib
import math
from pathlib import Path
from typing import List, Optional, Dict
from dataclasses import dataclass

import yaml
import psycopg2
from pynput import keyboard
from PyQt6.QtWidgets import (
    QApplication, QWidget, QVBoxLayout, QHBoxLayout,
    QLineEdit, QListWidget, QListWidgetItem, QLabel,
    QComboBox, QPushButton, QListView, QCompleter
)
from PyQt6.QtCore import QStringListModel
from PyQt6.QtCore import Qt, QSize, pyqtSignal, QObject, QTimer, QPointF, QPropertyAnimation, QEasingCurve
from PyQt6.QtGui import QKeySequence, QShortcut, QCursor, QIcon, QPixmap, QImage, QPainter, QColor, QPen, QWheelEvent
from pyrogram import Client
from PIL import Image
import io


@dataclass
class Sticker:
    sticker_id: str
    file_id: str
    text: str
    set_name: str
    emoji: str


@dataclass
class ChatInfo:
    id: int
    name: str
    type: str


class SmoothScrollListWidget(QListWidget):
    """QListWidget with smooth scrolling animation"""

    def __init__(self, parent=None):
        super().__init__(parent)
        self._scroll_animation = QPropertyAnimation(self.verticalScrollBar(), b"value")
        self._scroll_animation.setEasingCurve(QEasingCurve.Type.OutCubic)
        self._scroll_animation.setDuration(300)
        self._target_value = 0
        self._scroll_step = 120  # pixels per scroll step

    def wheelEvent(self, event: QWheelEvent):
        # Get scroll delta
        delta = event.angleDelta().y()
        if delta == 0:
            return

        scrollbar = self.verticalScrollBar()

        # Calculate target position
        if self._scroll_animation.state() == QPropertyAnimation.State.Running:
            # If animation is running, add to the target
            current_target = self._target_value
        else:
            current_target = scrollbar.value()

        # Calculate new target (negative delta = scroll down)
        scroll_amount = -delta / 120 * self._scroll_step
        self._target_value = int(current_target + scroll_amount)

        # Clamp to valid range
        self._target_value = max(scrollbar.minimum(), min(self._target_value, scrollbar.maximum()))

        # Start animation
        self._scroll_animation.stop()
        self._scroll_animation.setStartValue(scrollbar.value())
        self._scroll_animation.setEndValue(self._target_value)
        self._scroll_animation.start()

        event.accept()


class SearchableChatSelector(QWidget):
    """Custom widget with QLineEdit + QCompleter for chat search"""

    # Signal emitted when user selects a chat
    chatSelected = pyqtSignal(object)  # emits ChatInfo object

    def __init__(self, parent=None):
        super().__init__(parent)
        self._chats: List[ChatInfo] = []
        self._chat_map: Dict[str, ChatInfo] = {}  # display_text -> ChatInfo
        self._selected_chat: Optional[ChatInfo] = None

        # Create line edit
        self._line_edit = QLineEdit()
        self._line_edit.setPlaceholderText("Search chats...")

        # Create completer
        self._model = QStringListModel()
        self._completer = QCompleter(self._model, self)
        self._completer.setCaseSensitivity(Qt.CaseSensitivity.CaseInsensitive)
        self._completer.setFilterMode(Qt.MatchFlag.MatchContains)
        self._completer.setCompletionMode(QCompleter.CompletionMode.PopupCompletion)
        self._line_edit.setCompleter(self._completer)

        # Connect selection
        self._completer.activated.connect(self._on_completer_activated)

        # Layout
        layout = QHBoxLayout()
        layout.setContentsMargins(0, 0, 0, 0)
        layout.addWidget(self._line_edit)
        self.setLayout(layout)

    def setChats(self, chats: List[ChatInfo]):
        """Set the list of chats"""
        self._chats = chats
        self._chat_map.clear()

        display_names = []
        for chat in chats:
            icon = {"private": "👤", "group": "👥", "supergroup": "👥", "channel": "📢"}.get(chat.type, "💬")
            display_name = f"{icon} {chat.name}"
            display_names.append(display_name)
            self._chat_map[display_name] = chat

        self._model.setStringList(display_names)

    def _on_completer_activated(self, text: str):
        """Handle selection from completer"""
        chat = self._chat_map.get(text)
        if chat:
            self._selected_chat = chat
            self.chatSelected.emit(chat)

    def selectedChat(self) -> Optional[ChatInfo]:
        """Get currently selected chat"""
        return self._selected_chat

    def setSelectedChat(self, chat: ChatInfo):
        """Set selected chat and update display"""
        self._selected_chat = chat
        if chat:
            icon = {"private": "👤", "group": "👥", "supergroup": "👥", "channel": "📢"}.get(chat.type, "💬")
            self._line_edit.setText(f"{icon} {chat.name}")

    def lineEdit(self) -> QLineEdit:
        """Get the line edit widget"""
        return self._line_edit

    def clear(self):
        """Clear selection and text"""
        self._line_edit.clear()
        self._selected_chat = None


class SignalBridge(QObject):
    show_window = pyqtSignal()
    chats_loaded = pyqtSignal(list)
    send_result = pyqtSignal(bool, str)
    sticker_thumb_loaded = pyqtSignal(str, QPixmap)  # file_id, pixmap


class StickerSearchApp(QWidget):
    def __init__(self, config: dict):
        super().__init__()
        self.config = config
        self.db_conn = None
        self.pyrogram: Optional[Client] = None
        self.loop: Optional[asyncio.AbstractEventLoop] = None
        self.chats: List[ChatInfo] = []
        self.selected_chat: Optional[ChatInfo] = None
        self.state_file = Path(__file__).parent / ".state.json"
        self.thumb_cache_dir = Path(__file__).parent / ".thumb_cache"
        self.thumb_cache_dir.mkdir(exist_ok=True)
        self.thumb_cache: Dict[str, QPixmap] = {}  # file_id -> pixmap
        self.pending_thumbs: set = set()  # file_ids currently being downloaded
        self.spinner_frames = self._create_spinner_frames()
        self.spinner_frame_idx = 0

        self.signal_bridge = SignalBridge()
        self.signal_bridge.show_window.connect(self._show_window)
        self.signal_bridge.chats_loaded.connect(self._on_chats_loaded)
        self.signal_bridge.send_result.connect(self._on_send_result)
        self.signal_bridge.sticker_thumb_loaded.connect(self._on_thumb_loaded)

        self.init_ui()
        self.init_db()
        self.load_state()
        self.init_pyrogram()
        self.start_hotkey_listener()
        self._start_spinner_timer()
        self._start_chat_detection_timer()

    def init_ui(self):
        self.setWindowTitle("Sticker Search")
        self.setWindowFlags(
            Qt.WindowType.FramelessWindowHint |
            Qt.WindowType.WindowStaysOnTopHint |
            Qt.WindowType.Tool
        )
        self.setFixedSize(700, 650)

        layout = QVBoxLayout()
        layout.setContentsMargins(10, 10, 10, 10)
        layout.setSpacing(8)

        # Chat selector with search
        chat_layout = QHBoxLayout()
        chat_label = QLabel("Chat:")
        chat_label.setFixedWidth(40)
        self.chat_selector = SearchableChatSelector()
        self.chat_selector.setMinimumWidth(350)
        self.chat_selector.chatSelected.connect(self.on_chat_selected)
        self.refresh_btn = QPushButton("↻")
        self.refresh_btn.setFixedWidth(30)
        self.refresh_btn.clicked.connect(self.refresh_chats)
        chat_layout.addWidget(chat_label)
        chat_layout.addWidget(self.chat_selector, 1)
        chat_layout.addWidget(self.refresh_btn)
        layout.addLayout(chat_layout)

        # Search input
        self.search_input = QLineEdit()
        self.search_input.setPlaceholderText("Search stickers... (Enter to send)")
        self.search_input.textChanged.connect(self.on_search_changed)
        self.search_input.returnPressed.connect(self.on_enter_pressed)
        layout.addWidget(self.search_input)

        # Results grid with smooth scrolling
        self.results_list = SmoothScrollListWidget()
        self.results_list.setViewMode(QListView.ViewMode.IconMode)
        self.results_list.setIconSize(QSize(200, 200))
        self.results_list.setSpacing(8)
        self.results_list.setResizeMode(QListView.ResizeMode.Adjust)
        self.results_list.setMovement(QListView.Movement.Static)
        self.results_list.setWrapping(True)
        self.results_list.itemClicked.connect(self.on_sticker_selected)
        layout.addWidget(self.results_list)

        # Status bar
        self.status_label = QLabel("Initializing...")
        self.status_label.setStyleSheet("color: #888; font-size: 11px;")
        layout.addWidget(self.status_label)

        self.setLayout(layout)

        # Dark theme
        self.setStyleSheet("""
            QWidget {
                background-color: #1e1e1e;
                color: #d4d4d4;
                font-family: 'JetBrains Mono', 'Fira Code', monospace;
                font-size: 14px;
            }
            QLineEdit {
                padding: 12px;
                font-size: 16px;
                border: 2px solid #3c3c3c;
                border-radius: 8px;
                background-color: #252526;
            }
            QLineEdit:focus {
                border-color: #007acc;
            }
            QComboBox {
                padding: 8px;
                border: 1px solid #3c3c3c;
                border-radius: 6px;
                background-color: #252526;
            }
            QComboBox::drop-down {
                border: none;
                width: 20px;
            }
            QComboBox QAbstractItemView {
                background-color: #252526;
                border: 1px solid #3c3c3c;
                selection-background-color: #094771;
            }
            QPushButton {
                padding: 8px;
                border: 1px solid #3c3c3c;
                border-radius: 6px;
                background-color: #252526;
            }
            QPushButton:hover {
                background-color: #3c3c3c;
            }
            QListWidget {
                border: 1px solid #3c3c3c;
                border-radius: 8px;
                background-color: #252526;
                outline: none;
            }
            QListWidget::item {
                padding: 4px;
                border: none;
                border-radius: 6px;
            }
            QListWidget::item:selected {
                background-color: #094771;
            }
            QListWidget::item:hover {
                background-color: #2a2d2e;
            }
        """)

        # Shortcuts
        QShortcut(QKeySequence(Qt.Key.Key_Escape), self).activated.connect(self.hide)
        QShortcut(QKeySequence(Qt.Key.Key_Down), self).activated.connect(self.select_next)
        QShortcut(QKeySequence(Qt.Key.Key_Up), self).activated.connect(self.select_prev)
        QShortcut(QKeySequence(Qt.Key.Key_Right), self).activated.connect(self.select_next)
        QShortcut(QKeySequence(Qt.Key.Key_Left), self).activated.connect(self.select_prev)

    def select_next(self):
        current = self.results_list.currentRow()
        if current < self.results_list.count() - 1:
            self.results_list.setCurrentRow(current + 1)

    def select_prev(self):
        current = self.results_list.currentRow()
        if current > 0:
            self.results_list.setCurrentRow(current - 1)

    def _create_spinner_frames(self, num_frames: int = 12) -> List[QPixmap]:
        """Create spinning wheel animation frames"""
        frames = []
        size = 200
        for i in range(num_frames):
            pixmap = QPixmap(size, size)
            pixmap.fill(Qt.GlobalColor.transparent)

            painter = QPainter(pixmap)
            painter.setRenderHint(QPainter.RenderHint.Antialiasing)

            center = QPointF(size / 2, size / 2)
            radius = size / 2 - 25

            for j in range(num_frames):
                angle = (360 / num_frames) * j - 90
                alpha = int(255 * ((j - i) % num_frames) / num_frames)
                color = QColor(150, 150, 150, alpha)
                pen = QPen(color)
                pen.setWidth(8)
                pen.setCapStyle(Qt.PenCapStyle.RoundCap)
                painter.setPen(pen)

                rad = math.radians(angle)
                x1 = center.x() + (radius - 10) * math.cos(rad)
                y1 = center.y() + (radius - 10) * math.sin(rad)
                x2 = center.x() + radius * math.cos(rad)
                y2 = center.y() + radius * math.sin(rad)
                painter.drawLine(QPointF(x1, y1), QPointF(x2, y2))

            painter.end()
            frames.append(pixmap)
        return frames

    def _start_spinner_timer(self):
        """Start timer to animate loading spinners"""
        self.spinner_timer = QTimer()
        self.spinner_timer.timeout.connect(self._update_spinner)
        self.spinner_timer.start(80)

    def _update_spinner(self):
        """Update spinner animation for loading items"""
        if not self.pending_thumbs:
            return

        self.spinner_frame_idx = (self.spinner_frame_idx + 1) % len(self.spinner_frames)
        spinner_icon = QIcon(self.spinner_frames[self.spinner_frame_idx])

        for i in range(self.results_list.count()):
            item = self.results_list.item(i)
            sticker: Sticker = item.data(Qt.ItemDataRole.UserRole)
            if sticker and sticker.file_id in self.pending_thumbs:
                item.setIcon(spinner_icon)

    def _start_chat_detection_timer(self):
        """Start timer to detect current Telegram chat"""
        self._last_detected_chat = None
        self.chat_detection_timer = QTimer()
        self.chat_detection_timer.timeout.connect(self._update_detected_chat)
        self.chat_detection_timer.start(500)  # Check every 500ms

    def _update_detected_chat(self):
        """Update selected chat based on current Telegram window"""
        if not self.isVisible():
            return

        detected_chat = self._detect_telegram_chat()
        if detected_chat and detected_chat != self._last_detected_chat:
            self._last_detected_chat = detected_chat
            self._auto_select_chat(detected_chat)

    def init_db(self):
        try:
            db_config = self.config['database']
            self.db_conn = psycopg2.connect(
                host=db_config['host'],
                port=db_config['port'],
                user=db_config['user'],
                password=db_config['password'],
                dbname=db_config['dbname']
            )
            self.db_conn.autocommit = True
        except Exception as e:
            self.status_label.setText(f"DB error: {e}")

    def load_state(self):
        """Load saved state (selected chat)"""
        if self.state_file.exists():
            try:
                with open(self.state_file) as f:
                    state = json.load(f)
                    self._saved_chat_id = state.get('chat_id')
            except:
                self._saved_chat_id = None
        else:
            self._saved_chat_id = None

    def save_state(self):
        """Save state (selected chat)"""
        if self.selected_chat:
            with open(self.state_file, 'w') as f:
                json.dump({'chat_id': self.selected_chat.id}, f)

    def init_pyrogram(self):
        """Initialize Pyrogram in background thread"""
        def run():
            self.loop = asyncio.new_event_loop()
            asyncio.set_event_loop(self.loop)

            self.pyrogram = Client(
                "sticker_gui",
                api_id=self.config['telegram']['api_id'],
                api_hash=self.config['telegram']['api_hash'],
                workdir=str(Path(__file__).parent)
            )

            async def start_and_load():
                await self.pyrogram.start()
                await self._load_chats()

            self.loop.run_until_complete(start_and_load())
            self.loop.run_forever()

        thread = threading.Thread(target=run, daemon=True)
        thread.start()

    async def _load_chats(self):
        """Load recent dialogs"""
        chats = []
        async for dialog in self.pyrogram.get_dialogs(limit=50):
            chat = dialog.chat
            if chat.type.value in ('private', 'group', 'supergroup', 'channel'):
                name = chat.title or chat.first_name or "Unknown"
                if chat.last_name:
                    name += f" {chat.last_name}"
                chats.append(ChatInfo(
                    id=chat.id,
                    name=name,
                    type=chat.type.value
                ))
        self.signal_bridge.chats_loaded.emit(chats)

    def _on_chats_loaded(self, chats: List[ChatInfo]):
        """Handle chats loaded signal"""
        self.chats = chats
        self.chat_selector.setChats(chats)

        # Restore saved chat
        if self._saved_chat_id:
            for chat in chats:
                if chat.id == self._saved_chat_id:
                    self.chat_selector.setSelectedChat(chat)
                    self.selected_chat = chat
                    break

        self.status_label.setText("Ready • Ctrl+Shift+S to toggle")

    def refresh_chats(self):
        """Refresh chat list"""
        if self.pyrogram and self.loop:
            self.status_label.setText("Loading chats...")
            asyncio.run_coroutine_threadsafe(self._load_chats(), self.loop)

    def on_chat_selected(self, chat):
        """Handle chat selection from searchable combo"""
        if chat:
            self.selected_chat = chat
            self.save_state()

    def start_hotkey_listener(self):
        hotkey_str = self.config.get('hotkey', '<ctrl>+<shift>+s')

        def on_activate():
            self.signal_bridge.show_window.emit()

        self.hotkey_listener = keyboard.GlobalHotKeys({
            hotkey_str: on_activate
        })
        self.hotkey_listener.start()

    def _detect_telegram_chat(self) -> Optional[str]:
        """Detect current chat from Telegram window title"""
        try:
            result = subprocess.run(
                ['xdotool', 'search', '--class', 'TelegramDesktop', 'getwindowname'],
                capture_output=True,
                text=True,
                timeout=1
            )
            if result.returncode != 0 or not result.stdout.strip():
                return None

            title = result.stdout.strip().split('\n')[0]
            # Title format: "‎⁨chat_name⁩ – (number)" or just "chat_name – (number)"
            # Remove unicode control characters and extract name before " – "
            clean_title = re.sub(r'[\u200e\u2068\u2069]', '', title)
            if ' – ' in clean_title:
                chat_name = clean_title.split(' – ')[0].strip()
                return chat_name
            return None
        except Exception:
            return None

    def _auto_select_chat(self, chat_name: str):
        """Find and select chat by name"""
        if not chat_name or not self.chats:
            return

        chat_name_lower = chat_name.lower()
        for chat in self.chats:
            if chat.name.lower() == chat_name_lower:
                self.chat_selector.setSelectedChat(chat)
                self.selected_chat = chat
                return

    def _show_window(self):
        if self.isVisible():
            self.hide()
            return

        # Reset last detected chat to force re-detection
        self._last_detected_chat = None

        # Auto-detect current Telegram chat
        detected_chat = self._detect_telegram_chat()
        if detected_chat:
            self._last_detected_chat = detected_chat
            self._auto_select_chat(detected_chat)

        # Open on the monitor where cursor is located
        cursor_pos = QCursor.pos()
        screen = QApplication.screenAt(cursor_pos)
        if not screen:
            screen = QApplication.primaryScreen()
        screen_geo = screen.geometry()
        x = screen_geo.x() + (screen_geo.width() - self.width()) // 2
        y = screen_geo.y() + (screen_geo.height() - self.height()) // 3
        self.move(x, y)

        self.show()
        self.activateWindow()
        self.raise_()
        self.search_input.setFocus()
        self.search_input.selectAll()

    def on_search_changed(self, text: str):
        if len(text) < 2:
            self.results_list.clear()
            return

        stickers = self.search_stickers(text)
        self.update_results(stickers)

    def search_stickers(self, query: str) -> List[Sticker]:
        if not self.db_conn:
            return []

        user_id = self.config.get('user_id', 0)
        try:
            cur = self.db_conn.cursor()
            cur.execute("""
                SELECT sticker_id, file_id, text, set_name, emoji
                FROM stickers
                WHERE user_id = %s AND text ILIKE %s
                LIMIT 20
            """, (user_id, f"%{query}%"))

            result = [
                Sticker(
                    sticker_id=row[0],
                    file_id=row[1],
                    text=row[2] or "",
                    set_name=row[3] or "",
                    emoji=row[4] or ""
                )
                for row in cur.fetchall()
            ]
            cur.close()
            return result
        except Exception as e:
            self.db_conn.rollback()
            self.status_label.setText(f"Search error: {e}")
            return []

    def update_results(self, stickers: List[Sticker]):
        self.results_list.clear()
        for sticker in stickers:
            item = QListWidgetItem()
            item.setData(Qt.ItemDataRole.UserRole, sticker)
            item.setSizeHint(QSize(210, 210))

            # Set cached thumbnail or schedule download with spinner
            if sticker.file_id in self.thumb_cache:
                item.setIcon(QIcon(self.thumb_cache[sticker.file_id]))
            else:
                # Show spinner while loading
                item.setIcon(QIcon(self.spinner_frames[self.spinner_frame_idx]))
                if sticker.file_id not in self.pending_thumbs and self.pyrogram and self.loop:
                    self.pending_thumbs.add(sticker.file_id)
                    asyncio.run_coroutine_threadsafe(
                        self._download_sticker_thumb(sticker.file_id),
                        self.loop
                    )

            self.results_list.addItem(item)

        if stickers:
            self.results_list.setCurrentRow(0)

    def on_enter_pressed(self):
        if self.results_list.count() > 0:
            current = self.results_list.currentRow()
            if current < 0:
                current = 0
            item = self.results_list.item(current)
            if item:
                self.on_sticker_selected(item)

    def on_sticker_selected(self, item: QListWidgetItem):
        sticker: Sticker = item.data(Qt.ItemDataRole.UserRole)
        if not sticker:
            return

        if not self.selected_chat:
            self.status_label.setText("⚠ Select a chat first!")
            return

        if not self.pyrogram or not self.loop:
            self.status_label.setText("⚠ Telegram not connected")
            return

        self.status_label.setText("Sending...")
        asyncio.run_coroutine_threadsafe(
            self._send_sticker(sticker),
            self.loop
        )

    async def _send_sticker(self, sticker: Sticker):
        try:
            await self.pyrogram.send_sticker(
                self.selected_chat.id,
                sticker.file_id
            )
            self.signal_bridge.send_result.emit(True, self.selected_chat.name)
        except Exception as e:
            self.signal_bridge.send_result.emit(False, str(e))

    def _get_thumbnail_from_db(self, file_id: str) -> Optional[bytes]:
        """Get thumbnail from database if exists"""
        if not self.db_conn:
            return None
        try:
            cur = self.db_conn.cursor()
            cur.execute("SELECT thumbnail FROM sticker_thumbnails WHERE file_id = %s", (file_id,))
            row = cur.fetchone()
            cur.close()
            return row[0] if row else None
        except Exception:
            return None

    async def _download_sticker_thumb(self, file_id: str):
        """Download sticker thumbnail and emit signal when ready"""
        try:
            thumb_size = 200

            # Check disk cache first
            cache_file = self.thumb_cache_dir / f"{hashlib.md5(file_id.encode()).hexdigest()}.png"
            if cache_file.exists():
                pixmap = QPixmap(str(cache_file))
                if not pixmap.isNull():
                    pixmap = pixmap.scaled(thumb_size, thumb_size, Qt.AspectRatioMode.KeepAspectRatio, Qt.TransformationMode.SmoothTransformation)
                    self.signal_bridge.sticker_thumb_loaded.emit(file_id, pixmap)
                    return

            # Check database for thumbnail
            db_thumb = self._get_thumbnail_from_db(file_id)
            if db_thumb:
                qimg = QImage.fromData(db_thumb)
                pixmap = QPixmap.fromImage(qimg)
                if not pixmap.isNull():
                    pixmap = pixmap.scaled(thumb_size, thumb_size, Qt.AspectRatioMode.KeepAspectRatio, Qt.TransformationMode.SmoothTransformation)
                    # Save to disk cache for faster access next time
                    pixmap.save(str(cache_file), "PNG")
                    self.signal_bridge.sticker_thumb_loaded.emit(file_id, pixmap)
                    return

            # Download sticker file from Telegram
            sticker_data = await self.pyrogram.download_media(file_id, in_memory=True)
            if not sticker_data:
                return

            # Convert webp to PNG using PIL (keep original size for quality)
            img = Image.open(io.BytesIO(sticker_data.getvalue()))

            # Convert to QPixmap
            buffer = io.BytesIO()
            img.save(buffer, format='PNG')
            buffer.seek(0)

            qimg = QImage.fromData(buffer.getvalue())
            pixmap = QPixmap.fromImage(qimg)

            if not pixmap.isNull():
                # Save full resolution to disk cache
                pixmap.save(str(cache_file), "PNG")
                # Scale for display with high quality
                scaled = pixmap.scaled(thumb_size, thumb_size, Qt.AspectRatioMode.KeepAspectRatio, Qt.TransformationMode.SmoothTransformation)
                self.signal_bridge.sticker_thumb_loaded.emit(file_id, scaled)

        except Exception as e:
            pass  # Silently fail for thumbnails
        finally:
            self.pending_thumbs.discard(file_id)

    def _on_thumb_loaded(self, file_id: str, pixmap: QPixmap):
        """Handle sticker thumbnail loaded signal"""
        self.thumb_cache[file_id] = pixmap

        # Update list items with this file_id
        for i in range(self.results_list.count()):
            item = self.results_list.item(i)
            sticker: Sticker = item.data(Qt.ItemDataRole.UserRole)
            if sticker and sticker.file_id == file_id:
                item.setIcon(QIcon(pixmap))

    def _on_send_result(self, success: bool, message: str):
        if success:
            self.status_label.setText(f"✓ Sent to {message}")
            self.hide()
        else:
            self.status_label.setText(f"✗ Error: {message}")

    def closeEvent(self, event):
        if self.db_conn:
            self.db_conn.close()
        if self.pyrogram and self.loop:
            asyncio.run_coroutine_threadsafe(self.pyrogram.stop(), self.loop)
            self.loop.call_soon_threadsafe(self.loop.stop)
        event.accept()


def main():
    config_path = Path(__file__).parent / "config.yaml"
    if not config_path.exists():
        print(f"Config not found: {config_path}")
        sys.exit(1)

    with open(config_path) as f:
        config = yaml.safe_load(f)

    # Validate config
    if not config.get('telegram', {}).get('api_id'):
        print("Error: telegram.api_id is required")
        print("Get it from https://my.telegram.org")
        sys.exit(1)

    if not config.get('telegram', {}).get('api_hash'):
        print("Error: telegram.api_hash is required")
        print("Get it from https://my.telegram.org")
        sys.exit(1)

    if not config.get('user_id'):
        print("Error: user_id is required")
        sys.exit(1)

    app = QApplication(sys.argv)
    app.setQuitOnLastWindowClosed(False)

    signal.signal(signal.SIGINT, lambda *args: app.quit())

    window = StickerSearchApp(config)

    print("Sticker Search GUI started")
    print(f"Press {config.get('hotkey', 'Ctrl+Shift+S')} to show")
    print("Ctrl+C to quit")

    quit_shortcut = QShortcut(QKeySequence("Ctrl+Q"), window)
    quit_shortcut.activated.connect(app.quit)

    timer = QTimer()
    timer.timeout.connect(lambda: None)
    timer.start(100)

    sys.exit(app.exec())


if __name__ == "__main__":
    main()
