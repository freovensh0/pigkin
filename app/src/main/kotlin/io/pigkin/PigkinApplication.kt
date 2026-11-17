package io.pigkin

import android.app.Application
import io.pigkin.ui.theme.LauncherIcon
import io.pigkin.ui.theme.PigkinFonts
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

class PigkinApplication : Application() {
  internal lateinit var graph: AppGraph
    private set

  override fun onCreate() {
    super.onCreate()
    // The :inference process only hosts a local model and builds none of the app.
    if (getProcessName() != packageName) return
    AppStrings.init(this)
    graph = AppGraph(this)
    // Started with the process rather than the activity, so the fonts are usually loaded by the first frame.
    graph.scope.launch(Dispatchers.Default) { PigkinFonts.preload(this@PigkinApplication) }
    graph.scope.launch(Dispatchers.IO) { LauncherIcon.follow(this@PigkinApplication, graph.preferences.theme) }
  }
}
